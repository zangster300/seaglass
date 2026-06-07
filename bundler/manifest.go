package bundler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"seaglass/web"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"
)

// Manifest maps unhashed asset URL paths to their hashed output URL paths
// e.g. "/static/js/index.js" -> "/static/js/index-B9D4HZ2M.js"
type Manifest map[string]string

// https://esbuild.github.io/api/#metafile
type metafile struct {
	Outputs map[string]metafileOutput `json:"outputs"`
}

type metafileOutput struct {
	EntryPoint string `json:"entryPoint"`
	CSSBundle  string `json:"cssBundle"`
}

// records esbuild's entry point -> hashed output mapping
func (m Manifest) addFromMetafile(data string) error {
	var meta metafile
	if err := json.Unmarshal([]byte(data), &meta); err != nil {
		return fmt.Errorf("failed to unmarshal metafile: %w", err)
	}

	for outPath, out := range meta.Outputs {
		if out.EntryPoint == "" {
			continue
		}

		relOut := strings.TrimPrefix(outPath, web.StaticDirectoryPath+"/")
		relSrc := strings.TrimPrefix(out.EntryPoint, web.ResourcesDirectoryPath+"/")

		// key by the output extension
		stem := strings.TrimSuffix(relSrc, path.Ext(relSrc))
		m[staticURL(stem+path.Ext(relOut))] = staticURL(relOut)
		if path.Ext(relSrc) != path.Ext(relOut) {
			m[staticURL(relSrc)] = staticURL(relOut)
		}

		if out.CSSBundle != "" {
			cssOut := strings.TrimPrefix(out.CSSBundle, web.StaticDirectoryPath+"/")
			m[staticURL(stem+".css")] = staticURL(cssOut)
		}
	}

	slog.Debug("manifest", "entries", m)
	return nil
}

// replace unhashed URLs with hashed counterparts
func (m Manifest) rewrite(doc []byte) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	// sort keys in longest-first order
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })

	pairs := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		pairs = append(pairs, k, m[k])
	}

	return []byte(strings.NewReplacer(pairs...).Replace(string(doc)))
}

func injectReloadScript(doc []byte, src string) []byte {
	tag := []byte(strings.Repeat(" ", 4) + `<script type="module" src="` + src + `"></script>`)

	// insert the reload client before </head>
	i := bytes.Index(doc, []byte("</head>"))
	if i < 0 {
		// or append it if no closing head tag is present
		slog.Warn("no </head> tag found, appending reload script")
		return append(doc, tag...)
	}

	// len(html document) + len(reload script tag) + len(newline byte)
	out := make([]byte, 0, len(doc)+len(tag)+1)
	out = append(out, doc[:i]...)
	out = append(out, tag...)
	out = append(out, '\n')
	return append(out, doc[i:]...)
}

// inserts a content hash esbuild style
func hashedName(rel string, data []byte) string {
	sum := xxhash.Sum64(data)
	hash := fmt.Sprintf("%08x", sum)
	ext := filepath.Ext(rel)
	return strings.TrimSuffix(rel, ext) + "-" + hash + ext
}

func staticURL(rel string) string {
	return web.StaticURLPrefix + filepath.ToSlash(rel)
}
