package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Raina-Hardik/smelt/internal/config"
	"github.com/Raina-Hardik/smelt/internal/ffmpeg"
	"github.com/Raina-Hardik/smelt/internal/scanner"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/semaphore"
)

type Pool struct {
	cfg *config.Config
	sem *semaphore.Weighted
}

func New(cfg *config.Config) *Pool {
	return &Pool{
		cfg: cfg,
		sem: semaphore.NewWeighted(int64(cfg.Workers)),
	}
}

// Run transcodes all files, logging results via zerolog. Blocks until done.
func (p *Pool) Run(ctx context.Context, files []scanner.MediaFile) error {
	results := make(chan error, len(files))
	var wg sync.WaitGroup

	for _, f := range files {
		f := f
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.sem.Acquire(ctx, 1); err != nil {
				results <- fmt.Errorf("%s: %w", f.Path, err)
				return
			}
			defer p.sem.Release(1)

			log.Info().Str("file", f.Path).Msg("starting")
			err := p.transcode(ctx, f, func(ev ffmpeg.ProgressEvent) {
				log.Debug().
					Str("file", filepath.Base(ev.FilePath)).
					Int("pct", int(ev.Percent*100)).
					Msg("progress")
			})
			if err != nil {
				log.Error().Err(err).Str("file", f.Path).Msg("failed")
				results <- err
			} else {
				log.Info().Str("file", f.Path).Msg("done")
				results <- nil
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var errCount int
	for err := range results {
		if err != nil {
			errCount++
		}
	}
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
	var wg sync.WaitGroup
	for _, f := range files {
		f := f
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.sem.Acquire(ctx, 1); err != nil {
				if onComplete != nil {
					onComplete(f, err)
				}
				return
			}
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
		Codec:  p.cfg.Codec,
		CRF:    p.cfg.CRF,
		Preset: p.cfg.Preset,
	}
	if err = ffmpeg.Run(ctx, f.Path, tmp, spec, onProgress); err != nil {
		return err
	}

	if err = os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, dst, err)
	}
	return nil
}

// transientPath is where ffmpeg writes while encoding. It sits in the final
// destination's directory (named after the source) so the success rename stays
// on one filesystem and never collides with the source or final files.
func transientPath(dst, src string) string {
	ext := filepath.Ext(src)
	base := strings.TrimSuffix(filepath.Base(src), ext)
	return filepath.Join(filepath.Dir(dst), base+transientSuffix+ext)
}

// OutputPath returns the final destination for a finished transcode:
//   - --inplace:     the original path (replaced)
//   - --output-dir:  the source's relative path mirrored under that dir (original name)
//   - otherwise:     <name><Suffix><ext> alongside the source (default .smelt)
func OutputPath(f scanner.MediaFile, cfg *config.Config) string {
	if cfg.InPlace {
		return f.Path
	}
	if cfg.OutputDir != "" {
		return filepath.Join(cfg.OutputDir, f.RelPath)
	}
	ext := filepath.Ext(f.Path)
	return strings.TrimSuffix(f.Path, ext) + cfg.Suffix + ext
}
