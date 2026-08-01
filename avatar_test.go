package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestGravatarURLNormalizesEmailAndUsesSHA256(t *testing.T) {
	const hash = "973dfe463ec85785f5f95af5ba3906eedb2d931c24e69824a89ea65dba4e813b"

	got := gravatarURL("  Test@Example.COM  ", 96)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(parsed.Path, "/"+hash) {
		t.Fatalf("path = %q, want SHA-256 hash %q", parsed.Path, hash)
	}
	if parsed.Query().Get("s") != "96" || parsed.Query().Get("d") != "identicon" || parsed.Query().Get("r") != "g" {
		t.Fatalf("unexpected query: %q", parsed.RawQuery)
	}
	if got != gravatarURL("test@example.com", 96) {
		t.Fatal("equivalent normalized emails produced different URLs")
	}
}

func TestGravatarURLWithoutEmailUsesStablePlaceholderHash(t *testing.T) {
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := gravatarURL("", 96); !strings.Contains(got, emptySHA256) {
		t.Fatalf("URL %q does not contain the empty-email SHA-256 hash", got)
	}
}
