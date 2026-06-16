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

func (p *Pool) transcode(ctx context.Context, f scanner.MediaFile, onProgress func(ffmpeg.ProgressEvent)) error {
	dst := OutputPath(f.Path)
	if err := ffmpeg.Run(ctx, f.Path, dst, p.cfg.Codec, onProgress); err != nil {
		return err
	}
	if p.cfg.InPlace {
		if err := os.Rename(dst, f.Path); err != nil {
			return fmt.Errorf("inplace rename: %w", err)
		}
	}
	return nil
}

// OutputPath returns the default output path for a given source file.
func OutputPath(src string) string {
	ext := filepath.Ext(src)
	return strings.TrimSuffix(src, ext) + ".transcoded" + ext
}
