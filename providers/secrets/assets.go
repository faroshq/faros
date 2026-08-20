// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
)

// portalFS embeds the built Vite bundle. The all: prefix is required so
// dotfiles are bundled and the embed doesn't fail on an otherwise-empty
// dist/ containing only .gitkeep on a fresh clone.
//
//go:embed all:portal/dist
var portalFS embed.FS

// portalHandler returns a file server over the embedded portal bundle plus
// the sub-FS so callers can serve index.html without the prefix.
func portalHandler() (http.Handler, fs.FS, error) {
	distFS, err := fs.Sub(portalFS, "portal/dist")
	if err != nil {
		return nil, nil, err
	}
	return http.FileServer(http.FS(distFS)), distFS, nil
}

// servePortalAsset writes the named asset from distFS, returning false on
// fs.ErrNotExist so the caller can fall through to the SPA index. It sets
// Content-Type from the extension (auto-sniffing doesn't apply when copying
// bytes manually) and no-cache so a provider upgrade propagates immediately.
func servePortalAsset(w http.ResponseWriter, _ *http.Request, distFS fs.FS, name string) bool {
	f, err := distFS.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.Copy(w, f)
	return true
}
