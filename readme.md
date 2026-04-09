# chicha-astro-control wiki (RU)

## Назначение
`chicha-astro-control` — desktop-приложение на Go + WebView для мониторинга дискретных входов DI и управления дискретными выходами DO на платформах Vecow-класса.

## Что делает приложение
- Показывает 8 входов `DI1..DI8` в реальном времени.
- Управляет 8 выходами `DO11..DO18`:
  - ON/OFF;
  - software PWM (`0..100`).
- Сохраняет пользовательские подписи каналов и состояние выходов в JSON-конфиг.
- Открывает ссылку репозитория во внешнем браузере.

## Локализация интерфейса
Поддерживаются языки:
- русский (`ru`)
- английский (`en`)
- немецкий (`de`)
- французский (`fr`)
- японский (`ja`)
- украинский (`uk`)

Как это работает:
1. Переводы хранятся в `static/translations.json`.
2. Файл встроен в бинарник через Go `embed` (папка `static/*`).
3. На старте UI выбирается язык по настройкам ОС (`navigator.languages` / `navigator.language`).
4. Если язык не поддерживается, выбирается английский (`en`) по умолчанию.

Переведены все основные элементы интерфейса:
- заголовки;
- подписи/подсказки;
- статусные сообщения;
- тексты окон/панелей;
- подсказки по пинам и форматам таймера.

## Сборка
### Linux / macOS
```bash
go build -o /usr/local/bin/chicha-astro-control chicha-astro-control.go
chmod +x /usr/local/bin/chicha-astro-control
chicha-astro-control
```

### Windows
```bash
GOOS=windows GOARCH=amd64 go build -ldflags="-H windowsgui" -o chicha-astro-control.exe chicha-astro-control.go
```

## Параметры запуска
- `-DI` — шаблон пути к DI (`%d` обязателен)
- `-DO` — шаблон пути к DO (`%d` обязателен)
- `-config` — путь к JSON-файлу настроек

## Значения по умолчанию
- Linux: `/sys/class/gpio/gpio%d/value`
- Windows: `C:\Vecow\ECX1K\di%d.value` и `C:\Vecow\ECX1K\do%d.value`
- macOS: `/tmp/astro-control/di%d.value` и `/tmp/astro-control/do%d.value`

## Быстрая памятка по пинам
- `DI1..DI8` → пины `1..8`
- `DI_COM` → пин `9`
- `DIO_GND` → пины `10` и `19`
- `DO11..DO18` → пины `11..18`
- `External VDC` → пин `20`
