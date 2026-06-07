package bundler

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"seaglass/web"
	"time"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/fsnotify/fsnotify"
)

type BuildConfig struct {
	Addr         string // host:port
	Directory    string // web resources root directory
	Endpoint     string // endpoint to hit to trigger a reload
	ShouldReload bool
}

func Build(cfg *BuildConfig) error {

	if err := os.RemoveAll(web.StaticDirectoryPath); err != nil {
		return fmt.Errorf("failed to remove static directory: %w", err)
	}

	opts := api.BuildOptions{
		Bundle:            true,
		EntryNames:        "[dir]/[name]-[hash]",
		ChunkNames:        "chunks/[name]-[hash]",
		AssetNames:        "[dir]/[name]-[hash]",
		Format:            api.FormatESModule,
		LogLevel:          api.LogLevelInfo,
		Metafile:          true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		MinifyWhitespace:  true,
		Outbase:           cfg.Directory,
		Outdir:            web.StaticDirectoryPath,
		Sourcemap:         api.SourceMapLinked,
		Target:            api.ESNext,
		Write:             true,
	}

	manifest := Manifest{}
	var htmlFiles []string

	err := filepath.WalkDir(cfg.Directory, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		switch filepath.Ext(path) {
		case ".ts", ".js":
			if !cfg.ShouldReload && path == web.ReloadScriptPath {
				return nil
			}
			opts.EntryPoints = append(opts.EntryPoints, path)
		case ".css":
			opts.EntryPoints = append(opts.EntryPoints, path)
		case ".html":
			htmlFiles = append(htmlFiles, path)
		default:
			// hash and copy all other files, mirroring cfg.Directory
			rel, err := filepath.Rel(cfg.Directory, path)
			if err != nil {
				return err
			}

			hashedRel, err := copyHashed(path, rel)
			if err != nil {
				return err
			}
			manifest[staticURL(rel)] = staticURL(hashedRel)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk directory for build: %w", err)
	}

	slog.Info("building")
	result := api.Build(opts)

	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			slog.Error("build error", "text", e.Text, "location", e.Location)
		}
		// skip reload ping
		return fmt.Errorf("esbuild reported %d error(s)", len(result.Errors))
	}

	if err := manifest.addFromMetafile(result.Metafile); err != nil {
		return fmt.Errorf("failed to parse metafile: %w", err)
	}

	for _, path := range htmlFiles {
		if err := writeHTML(cfg, path, manifest); err != nil {
			return fmt.Errorf("failed to write html: %w", err)
		}
	}

	slog.Info("build complete", "warnings", len(result.Warnings))

	if cfg.ShouldReload {
		resp, err := http.Get(fmt.Sprintf("http://%s/%s", cfg.Addr, cfg.Endpoint))
		if err != nil {
			slog.Debug("reload ping failed", "error", err)
		}
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}

	return nil
}

func copyHashed(src, rel string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	// hash the file content
	hashedRel := hashedName(rel, data)
	dst := filepath.Join(web.StaticDirectoryPath, hashedRel)

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("failed to mkdir: %w", err)
	}

	// write file at src into the static directory under content-hashed name
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return hashedRel, nil
}

func writeHTML(cfg *BuildConfig, src string, manifest Manifest) error {
	rel, err := filepath.Rel(cfg.Directory, src)
	if err != nil {
		return err
	}

	doc, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// rewrite asset references to their hashed names
	doc = manifest.rewrite(doc)
	if cfg.ShouldReload {
		doc = injectReloadScript(doc, manifest[staticURL("js/reload.js")])
	}

	dst := filepath.Join(web.StaticDirectoryPath, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("failed to mkdir: %w", err)
	}

	if err := os.WriteFile(dst, doc, 0o644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func Watch(ctx context.Context, cfg *BuildConfig) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}
	defer watcher.Close()

	trigger := make(chan struct{}, 1)
	go func() {
		const debounce = 100 * time.Millisecond
		var timer <-chan time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-trigger:
				timer = time.After(debounce)
			case <-timer:
				timer = nil
				if err := Build(cfg); err != nil {
					slog.Error("rebuild failed", "error", err)
				}
			}
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if !event.Has(fsnotify.Write | fsnotify.Create | fsnotify.Remove | fsnotify.Rename) {
					continue
				}
				slog.Debug("fsnotify event", "event", event.Op, "name", event.Name)

				// register newly created directory subtrees
				if event.Has(fsnotify.Create) {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						if err := watchSubTree(watcher, event.Name); err != nil {
							slog.Warn("failed to watch new subtree", "path", event.Name, "error", err)
						}
					}
				}

				select {
				case trigger <- struct{}{}:
				default:
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Warn("watcher error", "error", err)
			}
		}
	}()

	if err := watchSubTree(watcher, cfg.Directory); err != nil {
		return fmt.Errorf("failed to watch %s: %w", cfg.Directory, err)
	}

	<-ctx.Done()
	return nil
}

func watchSubTree(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if err := watcher.Add(path); err != nil {
				return err
			}
			slog.Debug("watching", "directory", path)
		}
		return nil
	})
}
