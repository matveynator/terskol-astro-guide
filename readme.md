# terskol-astro-guide

Desktop-приложение на WebView для управления 10 DIO/DO портами.

## Что есть
- 10 портов (1..10).
- Для каждого порта:
  - ON / OFF
  - цветовая индикация: зеленый = включен, серый = выключен
  - поле подписи устройства + кнопка «Сохранить»
- Подписи сохраняются в `dio-labels.json`.

## Запуск

```bash
go run terskol-astro-guide.go
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
