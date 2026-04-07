package webview

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"
)

type Hint int

const (
	HintNone Hint = iota
)

type WebView interface {
	Destroy()
	SetTitle(title string)
	SetSize(width int, height int, hint Hint)
	Bind(name string, value interface{}) error
	Navigate(url string)
	Run()
}

type browserWebView struct {
	url  string
	once sync.Once
}

func New(_ bool) WebView {
	return &browserWebView{}
}

func (window *browserWebView) Destroy()                           {}
func (window *browserWebView) SetTitle(_ string)                  {}
func (window *browserWebView) SetSize(_, _ int, _ Hint)           {}
func (window *browserWebView) Bind(_ string, _ interface{}) error { return nil }
func (window *browserWebView) Navigate(url string)                { window.url = url }

func (window *browserWebView) Run() {
	window.once.Do(func() {
		_ = open(window.url)
	})
	select {}
}

func open(url string) error {
	if url == "" {
		return fmt.Errorf("empty url")
	}

	if runtime.GOOS == "darwin" {
		return exec.Command("open", url).Start()
	}
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "start", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}
