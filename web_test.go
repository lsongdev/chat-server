package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testFrontend() fs.FS {
	return fstest.MapFS{
		"index.html":    {Data: []byte(`<!doctype html><div id="root"></div>`)},
		"assets/app.js": {Data: []byte(`console.log("chat")`)},
	}
}

func TestStaticHandlerServesSPA(t *testing.T) {
	handler := newStaticHandler(testFrontend())

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("home returned %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `<div id="root">`) {
		t.Fatal("index.html did not render the React root")
	}
}

func TestStaticHandlerFallsBackForClientRoutes(t *testing.T) {
	handler := newStaticHandler(testFrontend())

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/invite/some-token", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("invite route returned %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `<div id="root">`) {
		t.Fatal("client route did not fall back to index.html")
	}
}

func TestStaticHandlerDeepFallbackForClientRoutes(t *testing.T) {
	handler := newStaticHandler(testFrontend())

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/invite/a/very/deep/route", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("deep invite route returned %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `<div id="root">`) {
		t.Fatal("deep client route did not fall back to index.html")
	}
}

func TestStaticHandlerServesExistingAsset(t *testing.T) {
	handler := newStaticHandler(testFrontend())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "chat") {
		t.Fatalf("asset was not served: %d %q", response.Code, response.Body.String())
	}
}
