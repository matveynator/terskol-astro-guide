# terskol-astro-guide

Приложение для управления 10 DIO - DI INPUT / DO OUTPUT портами. На Linux/macOS с CGO запускается встроенное окно WebView, на Windows работает как локальный HTTP-сервер (откройте URL в браузере).

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
- Linux/macOS + CGO: открывается встроенный WebView.
- Windows или сборка без CGO: приложение запускает HTTP-сервер и пишет URL в лог.
