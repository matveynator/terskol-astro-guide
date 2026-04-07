# terskol-astro-guide

Минимальное desktop-приложение с WebView + HTTP API для управления питанием первого DIO порта.

## Запуск

```bash
go get github.com/webview/webview
go run terskol-astro-guide.go
```

## API
- `GET /api/state` -> `{ "power": "on"|"off" }`
- `POST /api/power` с JSON `{ "power": "on"|"off" }`

## DIO ECX-1000-2G
По умолчанию запись идет в:
- `/sys/class/gpio/gpio0/value`

Можно поменять путь флагом:

```bash
go run terskol-astro-guide.go -dio-value-file /your/path/to/first/dio/value
```

## Локальная логика
- Linux: запись `1/0` в файл DIO value.
- Не Linux: состояние хранится в памяти (для отладки UI).
