# terskol-astro-guide

Минимальное desktop-приложение на Go + WebView.

## Что делает
- Поднимает локальный HTTP сервер.
- Открывает `webview` окно.
- Показывает один переключатель состояния `on/off`.
- Пишет подробный лог старта, HTTP запросов и жизненного цикла WebView.

## Запуск на macOS

```bash
go run .
```

## Что смотреть в логах

При успешном старте должны быть строки:
- `startup: preparing HTTP server ...`
- `startup: HTTP server ready ...`
- `startup: creating WebView window`
- `startup: WebView navigation started ...`

При запросах UI:
- `http: request started ...`
- `http: request finished ...`

## Если окно не открылось

1. Проверьте `CGO_ENABLED`:

```bash
go env CGO_ENABLED
```

Для обычного macOS окружения должно быть `1`.

2. Запустите повторно и проверьте, есть ли строка `startup: creating WebView window`.
   Если строка есть, а окно не видно, проблема обычно в системной графической среде/WebKit.

## Структура
- `terskol-astro-guide.go` — вся бизнес-логика приложения в одном файле.
- `static/index.html` — UI, встроенный через `go:embed`.
- `third_party/webview_go/` — локальная зависимость webview.
