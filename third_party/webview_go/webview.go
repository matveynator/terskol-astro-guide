package webview

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

type inMemoryWebView struct{}

func New(_ bool) WebView {
	return &inMemoryWebView{}
}

func (window *inMemoryWebView) Destroy()                           {}
func (window *inMemoryWebView) SetTitle(_ string)                  {}
func (window *inMemoryWebView) SetSize(_, _ int, _ Hint)           {}
func (window *inMemoryWebView) Bind(_ string, _ interface{}) error { return nil }
func (window *inMemoryWebView) Navigate(_ string)                  {}
func (window *inMemoryWebView) Run()                               {}
