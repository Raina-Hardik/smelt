package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/db"
	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/semaphore"
)

type Pool struct {
	cfg         *config.Config
	db          *db.DB // nil when DB is disabled
	sem         *semaphore.Weighted
	gate        *gate
	resolveOnce sync.Once
	encoder     string // resolved once per run via resolveEncoder
	backend     string
}

func New(cfg *config.Config, database *db.DB) *Pool {
	return &Pool{
		cfg:  cfg,
		db:   database,
		sem:  semaphore.NewWeighted(int64(cfg.Workers)),
		gate: newGate(),
	}
}

// TogglePause pauses or resumes dispatch and reports the new paused state.
// In-flight jobs are never interrupted; pausing only withholds new ones.
func (p *Pool) TogglePause() bool { return p.gate.toggle() }

// Paused reports whether dispatch is currently paused.
func (p *Pool) Paused() bool { return p.gate.paused() }

// resolveEncoder probes for the hardware encoder once (it spawns ffmpeg) and
// records the choice for all files in this run. Safe to call concurrently and
// repeatedly; only the first call probes, the rest see the cached result. It
// does not log — callers (CLI) log the choice; the TUI shows it on screen.
func (p *Pool) resolveEncoder(ctx context.Context) {
	p.resolveOnce.Do(func() {
		p.encoder, p.backend = ffmpeg.ResolveEncoder(ctx, p.cfg.Codec, p.cfg.HWAccel)
	})
}

// Resolve probes (once) and returns the concrete encoder and backend that this
// run will use. The TUI calls it before starting so the pre-flight screen can
// show e.g. "auto → hevc_nvenc" without re-probing when the pool runs.
func (p *Pool) Resolve(ctx context.Context) (encoder, backend string) {
	p.resolveEncoder(ctx)
	return p.encoder, p.backend
}

// Run transcodes all files, logging results via zerolog. Blocks until done.
// It is a thin wrapper over RunWithCallbacks so both entry points share one
// dispatch loop; the callbacks here just translate events into log lines.
func (p *Pool) Run(ctx context.Context, files []scanner.MediaFile) error {
	start := time.Now()
	enc, backend := p.Resolve(ctx)
	log.Info().
		Str("codec", p.cfg.Codec).
		Str("hwaccel", p.cfg.HWAccel).
		Str("encoder", enc).
		Str("backend", backend).
		Msg("encoder selected")

	var failed atomic.Int64

	p.RunWithCallbacks(ctx, files,
		func(ev ffmpeg.ProgressEvent) {
			log.Debug().
				Str("file", filepath.Base(ev.FilePath)).
				Int("pct", int(ev.Percent*100)).
				Msg("progress")
		},
		func(f scanner.MediaFile, err error) {
			if err != nil {
				failed.Add(1)
				log.Error().Err(err).Str("file", f.Path).Msg("failed")
			} else {
				log.Info().Str("file", f.Path).Msg("done")
			}
		},
	)

	errCount := int(failed.Load())
	log.Info().
		Int("ok", len(files)-errCount).
		Int("failed", errCount).
		Dur("elapsed", time.Since(start)).
		Msg("transcode complete")

	if errCount > 0 {
		return fmt.Errorf("%d file(s) failed to transcode", errCount)
	}
	return nil
}

// RunWithCallbacks is like Run but fires onProgress and onComplete for each
// file so callers (e.g. the TUI) can react to individual events.
func (p *Pool) RunWithCallbacks(
	ctx context.Context,
	files []scanner.MediaFile,
	onProgress func(ffmpeg.ProgressEvent),
	onComplete func(scanner.MediaFile, error),
) {
	p.resolveEncoder(ctx)
	var wg sync.WaitGroup
	// Sequential dispatcher: block at the pause gate, then take a worker slot,
	// then spawn. Pausing stops new dispatch here while in-flight jobs run on;
	// the semaphore still bounds concurrency.
	for _, f := range files {
		if err := p.gate.wait(ctx); err != nil {
			if onComplete != nil {
				onComplete(f, err)
			}
			continue
		}
		if err := p.sem.Acquire(ctx, 1); err != nil {
			if onComplete != nil {
				onComplete(f, err)
			}
			continue
		}
		f := f
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer p.sem.Release(1)
			err := p.transcode(ctx, f, onProgress)
			if onComplete != nil {
				onComplete(f, err)
			}
		}()
	}
	wg.Wait()
}

// transientSuffix names the in-progress artifact ffmpeg writes to. It never
// survives a run: renamed to the final path on success, deleted on any failure.
const transientSuffix = ".transcoded"

func (p *Pool) transcode(ctx context.Context, f scanner.MediaFile, onProgress func(ffmpeg.ProgressEvent)) (err error) {
	dst := OutputPath(f, p.cfg)
	if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}

	tmp := transientPath(dst, f.Path)
	// Delete the partial artifact on any failure, including context cancellation.
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	spec := ffmpeg.EncodeSpec{
		Codec:        p.cfg.Codec,
		CRF:          p.cfg.CRF,
		Preset:       p.cfg.Preset,
		AudioCodec:   p.cfg.AudioCodec,
		AudioBitrate: p.cfg.AudioBitrate,
		SubtitleMode: p.cfg.SubtitleMode,
		ExtraArgs:    p.cfg.ExtraArgs,
		Container:    strings.TrimPrefix(filepath.Ext(dst), "."),
		Encoder:      p.encoder,
		Backend:      p.backend,
	}

	start := time.Now()
	if err = ffmpeg.Run(ctx, f.Path, tmp, spec, onProgress); err != nil {
		p.record(f, dst, spec, time.Since(start), err)
		return err
	}

	// Preserve the source file's permission bits on the output so that
	// restricted files (e.g. 0600) are not silently opened up after transcode.
	if f.Mode != 0 {
		if chmodErr := os.Chmod(tmp, f.Mode.Perm()); chmodErr != nil {
			log.Warn().Err(chmodErr).Str("file", f.Path).Msg("could not preserve file permissions")
		}
	}

	if err = os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, dst, err)
	}
	p.record(f, dst, spec, time.Since(start), nil)
	return nil
}

// record persists a transcode outcome to the history DB (no-op when DB is nil).
func (p *Pool) record(f scanner.MediaFile, dst string, spec ffmpeg.EncodeSpec, elapsed time.Duration, transcodeErr error) {
	if p.db == nil {
		return
	}
	var outSize int64
	if fi, err := os.Stat(dst); err == nil {
		outSize = fi.Size()
	}
	status, errMsg := "done", ""
	if transcodeErr != nil {
		status = "failed"
		errMsg = transcodeErr.Error()
	}
	r := db.Record{
		SourcePath:  f.Path,
		SourceMtime: f.Mtime,
		SourceSize:  f.Size,
		OutputPath:  dst,
		OutputSize:  outSize,
		TargetCodec: spec.Codec,
		Encoder:     spec.Encoder,
		Backend:     spec.Backend,
		CRF:         spec.CRF,
		Preset:      spec.Preset,
		ElapsedMs:   elapsed.Milliseconds(),
		Status:      status,
		ErrorMsg:    errMsg,
		CompletedAt: time.Now(),
	}
	if err := p.db.Insert(r); err != nil {
		log.Warn().Err(err).Str("file", f.Path).Msg("db: failed to record transcode")
	}
}

// Plan filters a scanned file list down to what actually needs transcoding.
// Non-inplace runs drop (a) our own previously generated outputs
// (`*<suffix><ext>`) and (b) sources whose output already exists. In-place runs
// probe each file's video codec and drop those already in the target codec.
// cfg.Force disables all skipping. database may be nil (disables DB-based skip).
func Plan(ctx context.Context, files []scanner.MediaFile, cfg *config.Config, database *db.DB) (todo []scanner.MediaFile, skipped int) {
	if cfg.InPlace {
		return planInplace(files, cfg, database, func(p string) (string, error) {
			return ffmpeg.ProbeVideoCodec(ctx, p)
		})
	}
	for _, f := range files {
		if isOwnOutput(f.Path, cfg.Suffix) {
			skipped++
			continue
		}
		if !cfg.Force {
			if _, err := os.Stat(OutputPath(f, cfg)); err == nil {
				skipped++
				continue
			}
		}
		if len(cfg.SkipSourceCodecs) > 0 {
			if cur, err := ffmpeg.ProbeVideoCodec(ctx, f.Path); err == nil && isSkippedSource(cur, cfg.SkipSourceCodecs) {
				skipped++
				continue
			}
		}
		todo = append(todo, f)
	}
	return todo, skipped
}

// planInplace keeps only files that aren't already in the target codec. The
// probe is injected for testability. On probe failure (or an unknown target)
// the file is kept — better to attempt a transcode than to wrongly skip.
// DB-based skip (matched mtime) short-circuits the ffprobe call on re-runs.
func planInplace(files []scanner.MediaFile, cfg *config.Config, database *db.DB, probe func(string) (string, error)) (todo []scanner.MediaFile, skipped int) {
	if cfg.Force {
		return files, 0
	}
	target := ffmpeg.CodecName(cfg.Codec)
	needProbe := target != "" || len(cfg.SkipSourceCodecs) > 0
	for _, f := range files {
		// Transcoding a hardlinked file breaks the link (new inode) and doubles
		// disk usage; skip it when asked so seeded/deduped libraries stay intact.
		if cfg.SkipHardlinked && f.Links > 1 {
			skipped++
			continue
		}
		// DB fast-path: if we have a completed record for this exact file version,
		// skip without probing (avoids re-running ffprobe on every re-run).
		if database != nil && database.IsDone(f.Path, f.Mtime) {
			skipped++
			continue
		}
		if needProbe {
			if cur, err := probe(f.Path); err == nil {
				if target != "" && cur == target {
					skipped++
					continue
				}
				if isSkippedSource(cur, cfg.SkipSourceCodecs) {
					skipped++
					continue
				}
			}
		}
		todo = append(todo, f)
	}
	return todo, skipped
}

// isSkippedSource reports whether cur matches any codec name in the skip list.
func isSkippedSource(cur string, skipCodecs []string) bool {
	for _, s := range skipCodecs {
		if strings.ToLower(s) == cur || ffmpeg.CodecName(s) == cur {
			return true
		}
	}
	return false
}

// isOwnOutput reports whether path looks like a file smelt itself produced:
// either a finished output ("movie.smelt.mkv") or a leftover transient artifact
// from an interrupted run ("movie.transcoded.mkv").
func isOwnOutput(path, suffix string) bool {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if strings.HasSuffix(stem, transientSuffix) {
		return true
	}
	return suffix != "" && strings.HasSuffix(stem, suffix)
}

// transientPath is where ffmpeg writes while encoding. It sits in the final
// destination's directory (named after the source) so the success rename stays
// on one filesystem and never collides with the source or final files. Its
// extension matches the destination so ffmpeg selects the right muxer.
func transientPath(dst, src string) string {
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	return filepath.Join(filepath.Dir(dst), base+transientSuffix+filepath.Ext(dst))
}

// OutputPath returns the final destination for a finished transcode:
//   - --inplace:     the original path (replaced)
//   - --output-dir:  the source's relative path mirrored under that dir (original name)
//   - otherwise:     <name><Suffix><ext> alongside the source (default .smelt)
//
// The extension is the --to container when set, else the source's.
func OutputPath(f scanner.MediaFile, cfg *config.Config) string {
	if cfg.InPlace {
		return f.Path
	}
	ext := outExt(f.Path, cfg.Container)
	if cfg.OutputDir != "" {
		rel := strings.TrimSuffix(f.RelPath, filepath.Ext(f.RelPath)) + ext
		return filepath.Join(cfg.OutputDir, rel)
	}
	return strings.TrimSuffix(f.Path, filepath.Ext(f.Path)) + cfg.Suffix + ext
}

// outExt returns the output extension: the target container (with a leading
// dot) when set, otherwise the source file's extension.
func outExt(src, container string) string {
	if container != "" {
		return "." + container
	}
	return filepath.Ext(src)
}
