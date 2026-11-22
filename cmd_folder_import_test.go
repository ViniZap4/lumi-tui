package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinizap/lumi/tui-client/account"
	"github.com/vinizap/lumi/tui-client/config"
)

func TestFolderTokenMovesToAccountsAndLeavesFolder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/me" || r.Header.Get("X-Lumi-Token") != "secret" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(account.User{ID: "u1", Username: "alice"})
	}))
	defer srv.Close()
	root := t.TempDir()
	config.SaveFolderConfig(root, &config.FolderConfig{ServerURL: srv.URL, ServerToken: "secret"})

	f := &account.File{}
	err := migrateFolderToken(context.Background(), root, config.LoadFolderConfig(root),
		func() (*account.File, error) { return f, nil }, func(*account.File) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if a := f.Find(srv.URL, "alice"); a == nil || a.Token != "secret" {
		t.Fatalf("account not imported: %+v", f.Accounts)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".lumi", "config.yaml"))
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "server_token") {
		t.Fatalf("token still in folder:\n%s", data)
	}
}

func TestFolderTokenDeadIsScrubbedAndNetworkErrorKeepsIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	root := t.TempDir()
	config.SaveFolderConfig(root, &config.FolderConfig{ServerURL: srv.URL, ServerToken: "dead"})
	noop := func(*account.File) error { return nil }
	load := func() (*account.File, error) { return &account.File{}, nil }
	if err := migrateFolderToken(context.Background(), root, config.LoadFolderConfig(root), load, noop); err != nil {
		t.Fatal(err)
	}
	if config.LoadFolderConfig(root).ServerToken != "" {
		t.Fatal("dead token should be scrubbed")
	}
	srv.Close()

	config.SaveFolderConfig(root, &config.FolderConfig{ServerURL: srv.URL, ServerToken: "keep"})
	if err := migrateFolderToken(context.Background(), root, config.LoadFolderConfig(root), load, noop); err == nil {
		t.Fatal("network error expected")
	}
	if config.LoadFolderConfig(root).ServerToken != "keep" {
		t.Fatal("token must stay until the account is safely stored")
	}
}
