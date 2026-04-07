# terskol-astro-guide

Приложение для управления 10 DIO - DI INPUT / DO OUTPUT портами. На Linux, macOS и Windows запускается встроенное окно WebView (desktop app).

## Что есть
- 10 портов (1..10).
- Для каждого порта:
  - ON / OFF
  - цветовая индикация: зеленый = включен, серый = выключен
  - поле подписи устройства + кнопка «Сохранить»
- Подписи сохраняются в `dio-labels.json`.

## Запуск

```bash
go run .
```

## Флаги
- `-port` HTTP порт (по умолчанию `8765`)
- `-directory` локальная директория для статики
- `-dio-value-path-template` путь-шаблон для файла DIO (по умолчанию `/sys/class/gpio/gpio%d/value`)
- `-labels-file` файл подписей (по умолчанию `dio-labels.json`)

## API
- `GET /api/state`
- `POST /api/power` body: `{ "port": 1, "power": "on" }`
- `POST /api/label` body: `{ "port": 1, "label": "Pump" }`


## Платформы
- Linux/macOS/Windows + CGO: открывается встроенный WebView.
- Сборка без CGO не поддерживается, потому что WebView-биндинг использует cgo.

## Сборка Windows (WebView)
1. Установите компилятор C/C++ для Go cgo (например, `mingw-w64`).
2. Проверьте, что `gcc --version` работает в том же терминале, где запускаете Go.
3. Соберите приложение как GUI:

```bash
set CGO_ENABLED=1
go build -ldflags="-H windowsgui" -o terskol-astro-guide.exe .
```

Если видите ошибку `build constraints exclude all Go files`, почти всегда это означает, что cgo отключен или не найден C-компилятор.
