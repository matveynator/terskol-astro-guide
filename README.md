# chicha-astro-control

WebView desktop app for monitoring DI and controlling DO channels on Vecow-class DIO hosts.

## 1) What the app does

- Shows 8 DI channels (`DI1..DI8`) with:
  - signal state (`on` / `off`)
  - measured voltage text
  - estimated signal frequency (Hz)
- Controls 8 DO channels (`DO1..DO8`) with:
  - power state (`on` / `off`)
  - PWM duty (`0..100%`)
- Stores channel labels and output state in a JSON config file.
- Opens repository link from UI in external system browser.

Pin mapping used in UI:
- `DI1..DI8` → terminal block pins `1..8`
- `DO1..DO8` → terminal block pins `11..18`

## 2) Quick start

```bash
go run .
```

The app binds HTTP server to localhost, starting from port `7654`.  
If the port is busy, it increments (`7655`, `7656`, ...) until a free port is found.

## 3) Build

```bash
go build -o chicha-astro-control .
```

Windows example:

```bash
GOOS=windows GOARCH=amd64 go build -o chicha-astro-control.exe .
```

## 4) Runtime flags

Only three flags are supported:

- `-DI` — DI path template with `%d`
- `-DO` — DO path template with `%d`
- `-config` — path to JSON settings file

Examples:

```bash
go run . -DI "/sys/class/gpio/gpio%d/value" -DO "/sys/class/gpio/gpio%d/value"
```

```bash
go run . -config "./astro-settings.json"
```

If `-DI`/`-DO` are not provided, defaults depend on OS:

- Linux: `/sys/class/gpio/gpio%d/value`
- Windows: `C:\Vecow\ECX1K\di%d.value` and `C:\Vecow\ECX1K\do%d.value`
- macOS: `/tmp/astro-control/di%d.value` and `/tmp/astro-control/do%d.value`

## 5) HTTP API

- `GET /api/state`
- `POST /api/output/power`
  - body: `{ "channel": 1, "power": "on" }`
- `POST /api/output/pwm`
  - body: `{ "channel": 1, "pwm": 60 }`
- `POST /api/label`
  - body: `{ "kind": "output", "channel": 1, "label": "Pump" }`
- `POST /api/open/repository`
  - opens GitHub URL in external browser

## 6) Hardware notes

- DI and DO directions are treated as fixed runtime roles.
- Output voltage visualization uses a `0.0V..3.3V` scale derived from PWM duty.
