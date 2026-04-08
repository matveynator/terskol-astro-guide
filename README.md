# chicha-astro-control

Desktop-приложение на WebView для управления DIO на Vecow ECX-1000-2G.

## Что есть
- 8 входов DI (DI0..DI7), read-only индикация сигнала.
- Для DI показывается текущее напряжение и оценка частоты сигнала в Hz.
- 8 выходов DO (DO0..DO7), управление ON/OFF.
- Для каждого выхода есть PWM-крутилка duty 0..100%, а отображение уровня в UI показывается в вольтах.
- Переименование каналов через кнопку-карандаш и кнопку «Сохранить».
- Подписи сохраняются в `dio-labels.json`.
- В UI используется шкала напряжения выхода 0.0V..3.3V (расчет от PWM duty).
- В заголовках каналов используются реальные номера пинов terminal block:
  - DI0..DI7 → пины 1..8
  - DO0..DO7 → пины 11..18

## Запуск

```bash
go run .
```

## Сборка бинарников

```bash
go build -o chicha-astro-control .
```

```bash
GOOS=windows GOARCH=amd64 go build -o chicha-astro-control.exe .
```

## Флаги
- `-port` HTTP порт (по умолчанию `8765`)
- `-directory` локальная директория для статики
- `-dio-input-path-template` явный путь-шаблон DI с `%d` (приоритет выше OS default)
- `-dio-output-path-template` явный путь-шаблон DO с `%d` (приоритет выше OS default)
- `-dio-linux-input-path-template` Linux путь-шаблон DI (по умолчанию `/sys/class/gpio/gpio%d/value`)
- `-dio-linux-output-path-template` Linux путь-шаблон DO (по умолчанию `/sys/class/gpio/gpio%d/value`)
- `-dio-windows-input-path-template` Windows путь-шаблон DI (по умолчанию `C:\Vecow\ECX1K\di%d.value`)
- `-dio-windows-output-path-template` Windows путь-шаблон DO (по умолчанию `C:\Vecow\ECX1K\do%d.value`)
- `-labels-file` файл подписей (по умолчанию `dio-labels.json`)
- `-input-on-voltage` напряжение для цифрового состояния DI=1 (по умолчанию `24.0`)
- `-input-off-voltage` напряжение для цифрового состояния DI=0 (по умолчанию `0.0`)
- `-input-threshold-voltage` порог, по которому числовое значение DI считается активным (по умолчанию `2.0`)

## API
- `GET /api/state`
- `POST /api/output/power` body: `{ "channel": 1, "power": "on" }`
- `POST /api/output/pwm` body: `{ "channel": 1, "pwm": 60 }`
- `POST /api/label` body: `{ "kind": "output", "channel": 1, "label": "Pump" }`

## По документации ECX-1000
В режиме DIO доступны DI/DO фиксированного назначения. Для isolated DIO направление каналов аппаратно фиксировано (DI отдельно от DO), и в runtime не переключается.
Для non-isolated GPIO режимов логический уровень — 3.3V, поэтому в UI выходного PWM используется шкала от 0.0V до 3.3V.
