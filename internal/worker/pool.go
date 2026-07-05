package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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

// lookPath is exec.LookPath, indirected so tests can force either branch of
// GovernorHint without depending on what's actually installed on the runner.
var lookPath = exec.LookPath

// ffmpeg entry points, indirected so the per-file decode decision and the
// retry-once path are testable without spawning ffmpeg/ffprobe.
var (
	ffmpegRun         = ffmpeg.Run
	probeAttrs        = ffmpeg.ProbeAttrs
	probeDecode       = ffmpeg.ProbeDecode
	probeEncoder10Bit = ffmpeg.ProbeEncoder10Bit
)

type Pool struct {
	cfg         *config.Config
	db          *db.DB // nil when DB is disabled
	sem         *semaphore.Weighted
	gate        *gate
	resolveOnce sync.Once
	encoder     string // resolved once per run via resolveEncoder
	backend     string

	tenBitWarn       sync.Once // once-per-run WARN when main10 encode is unavailable
	decodeMissLogged sync.Map  // shape → struct{}; debug-log the first decode-probe miss per shape
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

// ResourceProfile describes the decode/encode split for a resolved encoder
// choice, independent of how it's displayed (CLI log line, TUI row). With a
// hardware encoder on a decode-capable backend and hwdecode=auto, decode runs
// on the same device per file (DecodeHW); otherwise decode is software, and
// Warn is true whenever that software decode would run concurrently with a
// hardware encode block — the most thermally demanding combination. Shared by
// the CLI (LogResourceProfile) and the TUI's pre-flight screen so both
// surfaces agree.
type ResourceProfile struct {
	Encoder       string
	Backend       string // "" = software encode
	DecodeThreads int    // 0 = uncapped (ffmpeg default)
	HWDecode      string // resolved --hwdecode mode (auto|off)
	DecodeHW      bool   // hardware decode will be attempted per file
	Warn          bool
}

// BuildResourceProfile resolves the profile for a given encoder/backend/cap.
func BuildResourceProfile(encoder, backend string, decodeThreads int, hwdecode string) ResourceProfile {
	r := ResourceProfile{Encoder: encoder, Backend: backend, DecodeThreads: decodeThreads, HWDecode: hwdecode}
	r.DecodeHW = backend != "" && !strings.EqualFold(hwdecode, "off") && ffmpeg.HWDecodeCapable(backend)
	r.Warn = backend != "" && !r.DecodeHW
	return r
}

// DecodeLabel is a short human-readable summary of the decode side, e.g.
// "hw: vaapi (per-file, may fall back)", "software (capped at 4 threads)" or
// "software (uncapped)". The hardware wording stays honest: probes run per
// file at dispatch time, so individual files may still decode in software.
func (r ResourceProfile) DecodeLabel() string {
	if r.DecodeHW {
		return fmt.Sprintf("hw: %s (per-file, may fall back)", r.Backend)
	}
	if r.DecodeThreads > 0 {
		s := "thread"
		if r.DecodeThreads != 1 {
			s = "threads"
		}
		return fmt.Sprintf("software (capped at %d %s)", r.DecodeThreads, s)
	}
	return "software (uncapped)"
}

// GovernorHint suggests a concrete next step for the resource-profile
// warning. smelt itself has no OS dependency beyond ffmpeg/ffprobe, and that
// must hold for this hint too: not every Linux uses systemd (e.g. Void's
// runit), and this warning also fires on Windows and macOS builds. So the
// only suggestion made unconditionally is smelt's own cross-platform lever
// (--decode-threads / --workers); a systemd-run example is appended only
// when systemd-run is actually present on $PATH.
func (r ResourceProfile) GovernorHint() string {
	threads := r.DecodeThreads
	if threads <= 0 {
		threads = 2
	}
	hint := fmt.Sprintf("lower --decode-threads (try %d) and/or --workers", threads)
	if runtime.GOOS == "linux" {
		if _, err := lookPath("systemd-run"); err == nil {
			hint += fmt.Sprintf("; or wrap the run: systemd-run --scope -p CPUQuota=50%% --nice=10 smelt transcode --decode-threads %d --workers 1 ...", threads)
		}
	}
	// Lead with the applicable hardware-decode remedy: the whole warning
	// disappears if decode moves onto the encoder's device.
	if r.Backend != "" {
		switch {
		case strings.EqualFold(r.HWDecode, "off"):
			hint = "remove --hwdecode off; or " + hint
		case !ffmpeg.HWDecodeCapable(r.Backend):
			hint = fmt.Sprintf("hardware decode is not supported on %s this release; ", r.Backend) + hint
		}
	}
	return hint
}

// LogResourceProfile logs the decode/encode split for a resolved encoder
// choice. Nothing here caps the combined load when Warn is true; the warning
// exists so it's visible before ffmpeg starts, not discovered via a thermal
// shutdown. Call once per run, including on the --dry-run path.
func LogResourceProfile(encoder, backend string, decodeThreads int, hwdecode string) {
	r := BuildResourceProfile(encoder, backend, decodeThreads, hwdecode)
	if r.DecodeHW {
		log.Info().Str("decode", r.DecodeLabel()).Str("encode", backend).Str("encoder", encoder).Msgf("full hardware pipeline: %s", backend)
		return
	}
	if !r.Warn {
		log.Info().Str("decode", r.DecodeLabel()).Str("encode", "software").Msg("resource profile")
		return
	}
	log.Warn().
		Str("decode", r.DecodeLabel()).
		Str("encode", backend).
		Str("encoder", encoder).
		Str("try", r.GovernorHint()).
		Msg("resource profile: software decode + hardware encode running concurrently; combined CPU+GPU load is not capped by smelt — see --decode-threads and --workers, or throttle at the OS level")
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
	LogResourceProfile(enc, backend, p.cfg.DecodeThreads, p.cfg.HWDecode)

	var failed atomic.Int64

	p.RunWithCallbacks(ctx, files,
		func(ev ffmpeg.ProgressEvent) {
			log.Debug().
				Str("file", filepath.Base(ev.FilePath)).
				Int("pct", int(ev.Percent*100)).
				Float64("speed", ev.Speed).
				Dur("eta", ev.ETA).
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

// RunTracked is like RunWithCallbacks but also upserts per-file progress into
// the history DB. runID must match a row already inserted via db.StartRun;
// call db.FinishRun after this returns to close the run record.
// onProgress and onComplete may be nil.
func (p *Pool) RunTracked(
	ctx context.Context,
	files []scanner.MediaFile,
	runID string,
	onProgress func(ffmpeg.ProgressEvent),
	onComplete func(scanner.MediaFile, error),
) {
	wrapped := func(ev ffmpeg.ProgressEvent) {
		if p.db != nil {
			speed := ""
			if ev.Speed > 0 {
				speed = fmt.Sprintf("%.2fx", ev.Speed)
			}
			_ = p.db.UpdateJob(runID, ev.FilePath, "running", ev.Percent, speed)
		}
		if onProgress != nil {
			onProgress(ev)
		}
	}
	wrappedDone := func(f scanner.MediaFile, err error) {
		if p.db != nil {
			status := "done"
			if err != nil {
				status = "failed"
			}
			_ = p.db.UpdateJob(runID, f.Path, status, 1.0, "")
		}
		if onComplete != nil {
			onComplete(f, err)
		}
	}
	p.RunWithCallbacks(ctx, files, wrapped, wrappedDone)
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
		Codec:         p.cfg.Codec,
		CRF:           p.cfg.CRF,
		Preset:        p.cfg.Preset,
		AudioCodec:    p.cfg.AudioCodec,
		AudioBitrate:  p.cfg.AudioBitrate,
		SubtitleMode:  p.cfg.SubtitleMode,
		DecodeThreads: p.cfg.DecodeThreads,
		ExtraArgs:     p.cfg.ExtraArgs,
		Container:     strings.TrimPrefix(filepath.Ext(dst), "."),
		Encoder:       p.encoder,
		Backend:       p.backend,
	}
	attrs := p.decideDecode(ctx, f.Path, &spec)

	start := time.Now()
	err = ffmpegRun(ctx, f.Path, tmp, spec, onProgress)
	if err != nil && spec.DecodeBackend != "" && ctx.Err() == nil {
		// Retry exactly once with software decode. No stderr classification —
		// one wasted attempt on a genuine encode failure is an acceptable cost
		// for not parsing ffmpeg output heuristically. The probe cache entry is
		// evicted so identical files re-probe instead of repeating this cycle.
		if _, isExec := err.(*ffmpeg.ExecError); isExec {
			_ = os.Remove(tmp)
			ffmpeg.EvictDecodeCache(spec.DecodeBackend, attrs)
			log.Warn().Err(err).Str("file", f.Path).Msg("hw decode failed, retrying with software decode")
			if spec.Backend == "nvenc" {
				log.Info().Msg("hint: consumer GeForce caps concurrent NVDEC/NVENC sessions; if retries recur at scale, lower --workers")
			}
			spec.DecodeBackend = ""
			err = ffmpegRun(ctx, f.Path, tmp, spec, onProgress)
		}
	}
	if err != nil {
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

// decideDecode settles the per-file video pipeline on spec: bit-depth routing
// (shapes the device can't encode faithfully take the full software pipeline)
// and the hardware-decode decision. Runs after the plan's skip decision, so
// skipped files never pay a probe. Returns the file's attrs so a runtime
// failure can evict the decode-probe cache entry. Any probe failure leaves
// spec untouched — software decode + the resolved encoder, exactly v0.20.0
// behavior.
func (p *Pool) decideDecode(ctx context.Context, path string, spec *ffmpeg.EncodeSpec) *ffmpeg.FileAttrs {
	if p.backend == "" {
		return nil
	}
	attrs, err := probeAttrs(ctx, path)
	if err != nil || attrs.PixFmt == "" {
		return nil
	}
	depth := ffmpeg.HWPipelineBitDepth(attrs.PixFmt)
	if depth == 0 {
		// 12-bit / 4:2:2 / 4:4:4 / exotic: the hardware pipeline's nv12/p010
		// surfaces would destroy chroma or depth — full software pipeline.
		p.softwarePipeline(spec)
		log.Info().Str("file", path).Str("pix_fmt", attrs.PixFmt).Msg("source shape unsupported by the hardware pipeline; using the software pipeline")
		return attrs
	}
	if depth == 10 {
		if !probeEncoder10Bit(ctx, p.encoder, p.backend) {
			p.tenBitWarn.Do(func() {
				log.Warn().Str("encoder", p.encoder).Msg("device cannot encode 10-bit (main10): 10-bit files take the full software pipeline instead of being silently flattened to 8-bit")
			})
			log.Info().Str("file", path).Msg("10-bit source without main10 encode: software pipeline")
			p.softwarePipeline(spec)
			return attrs
		}
		spec.SourceBitDepth = 10
	} else {
		spec.SourceBitDepth = 8
	}
	if strings.EqualFold(p.cfg.HWDecode, "off") {
		return attrs
	}
	if probeDecode(ctx, p.backend, path, attrs) {
		spec.DecodeBackend = p.backend
		if p.cfg.DecodeThreads > 0 {
			log.Debug().Str("file", path).Msg("--decode-threads omitted while hardware decode is active")
		}
	} else {
		shape := attrs.VideoCodec + "/" + attrs.Profile + "/" + attrs.PixFmt
		if _, seen := p.decodeMissLogged.LoadOrStore(shape, struct{}{}); !seen {
			log.Debug().Str("shape", shape).Str("backend", p.backend).Msg("source shape not hw-decodable on this device; software decode")
		}
	}
	return attrs
}

// softwarePipeline routes one file through the full software pipeline
// (software decode + software encode) without touching the run-level
// resolution other files use.
func (p *Pool) softwarePipeline(spec *ffmpeg.EncodeSpec) {
	spec.Encoder = ffmpeg.SoftwareEncoder(spec.Codec)
	spec.Backend = ""
	spec.DecodeBackend = ""
	spec.SourceBitDepth = 0
	// A preset chosen for the hardware encoder's namespace (e.g. nvenc p5)
	// would be rejected by the software encoder; snap to its default then.
	if !slices.Contains(ffmpeg.PresetsFor("", spec.Encoder), spec.Preset) {
		spec.Preset = ffmpeg.DefaultPreset("", spec.Encoder)
	}
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
//
// Unless cfg.AllowHDRLoss, every remaining candidate is also probed for a
// Dolby Vision RPU; smelt has no DV passthrough, so a plain re-encode would
// silently drop it. Matches are excluded and counted in blocked rather than
// silently degraded — the caller should surface that count so the user can
// re-run with --i-know-this-drops-hdr once they've made that call knowingly.
func Plan(ctx context.Context, files []scanner.MediaFile, cfg *config.Config, database *db.DB) (todo []scanner.MediaFile, skipped, blocked int) {
	if cfg.InPlace {
		todo, skipped = planInplace(files, cfg, database, func(p string) (string, error) {
			return ffmpeg.ProbeVideoCodec(ctx, p)
		})
	} else {
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
	}

	if cfg.AllowHDRLoss {
		return todo, skipped, 0
	}
	kept := todo[:0]
	for _, f := range todo {
		if isDV, err := ffmpeg.ProbeDolbyVision(ctx, f.Path); err == nil && isDV {
			blocked++
			log.Warn().Str("file", f.Path).Msg("blocked: Dolby Vision source; re-encode would silently drop the RPU layer — pass --i-know-this-drops-hdr to proceed anyway")
			continue
		}
		kept = append(kept, f)
	}
	return kept, skipped, blocked
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
