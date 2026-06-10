package main

import (
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

func TestBuildPromoteDests_IncludesWritableMounts(t *testing.T) {
	shared := t.TempDir()
	gdrive := t.TempDir() // writable external mount (stands in for rclone)

	cfg := &config.Config{}
	cfg.Stream.SharedDir = shared
	cfg.Stream.PromoteDirs = []config.PromoteDir{{Name: "Filmes", Path: t.TempDir()}}
	cfg.External.Mounts = []config.ExternalMount{
		{Name: "GDrive", Path: gdrive},                            // writable → included
		{Name: "PerUser", Path: t.TempDir(), UserSubpath: true},   // skipped (per-user)
		{Name: "Ghost", Path: filepath.Join(t.TempDir(), "nope")}, // not a dir → skipped
		{Name: "DupShared", Path: shared},                         // dedup vs sharedDir
	}

	dests := buildPromoteDests(cfg)

	byPath := map[string]string{}
	for _, d := range dests {
		byPath[d.Path] = d.Name
	}
	if byPath[gdrive] != "GDrive" {
		t.Errorf("writable mount GDrive should be a destination, got %v", dests)
	}
	if _, ok := byPath[shared]; ok {
		t.Error("sharedDir must not be duplicated (BuildPromoteDests adds it)")
	}
	for _, d := range dests {
		if d.Name == "PerUser" || d.Name == "Ghost" {
			t.Errorf("UserSubpath / non-existent mount should be skipped, got %q", d.Name)
		}
	}
}

func TestDirWritable(t *testing.T) {
	if !dirWritable(t.TempDir()) {
		t.Error("a fresh temp dir should be writable")
	}
	if dirWritable(filepath.Join(t.TempDir(), "does-not-exist")) {
		t.Error("a non-existent path should not be writable")
	}
	if dirWritable("") {
		t.Error("empty path should not be writable")
	}
}
