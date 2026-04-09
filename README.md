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


## 2) Linux & MacOS:

```bash
go build -o /usr/local/bin/chicha-astro-control chicha-astro-control.go; chmod +x /usr/local/bin/chicha-astro-control; chicha-astro-control; 
```

## 3) Windows:

```bash
GOOS=windows GOARCH=amd64 go build -ldflags="-H windowsgui" -o chicha-astro-control.exe chicha-astro-control.go
```

## 4) Optional runtime flags

Only three flags are supported:

- `-DI` — DI path template with `%d`
- `-DO` — DO path template with `%d`
- `-config` — path to JSON settings file


## 5) If `-DI`/`-DO` are not provided, defaults depend on OS:

- Linux: `/sys/class/gpio/gpio%d/value`
- Windows: `C:\Vecow\ECX1K\di%d.value` and `C:\Vecow\ECX1K\do%d.value`
- macOS: `/tmp/astro-control/di%d.value` and `/tmp/astro-control/do%d.value`

## 6) Hardware notes

- DI and DO directions are treated as fixed runtime roles.
- Output voltage visualization uses a `0.0V..3.3V` scale derived from PWM duty.

## 7) Wiki: ECX-1000-2G DIO/GPIO quick reference

This section is a compact wiring-oriented wiki for operators.

### 7.1 What is available on ECX-1000-2G

- ECX-1000 series documentation includes both **Isolated DIO** and **GPIO** sections.
- ECX-1000-2G model sheets are typically marked as **16 GPIO** variant.
- Always verify the exact SKU and rear-panel labeling before wiring.

### 7.2 Capabilities matrix (practical)

| Feature | Isolated DIO variant | GPIO variant |
|---|---|---|
| Signal type | Industrial isolated DI/DO | 3.3V logic GPIO |
| Direction | DI and DO fixed by hardware | Configurable by driver/API |
| Read input | High/Low state | High/Low state |
| Output control | High/Low state | High/Low state |
| Hardware PWM on pin | Not documented as native feature | Not documented as native feature |
| Hardware interrupt on pin | Not documented in DIO API | Not documented in public DIO/GPIO API |

### 7.3 Wiring guidance for Isolated DIO connector (20-pin)

- **Pins 1..8**: DI inputs.
- **Pin 9**: DI_COM (common for DI group).
- **Pin 10 and Pin 19**: DIO_GND (common ground).
- **Pins 11..18**: DO outputs.
- **Pin 20**: External VDC feed for DIO domain.

NPN/PNP reminder:
- **NPN (sink)**: DI_COM to `+V`, DI activates when pulled to `V-`.
- **PNP (source)**: DI_COM to `V-`, DI activates when `+V` is applied to DI pin.

### 7.4 About PWM and voltage measurement in this app

- The app exposes a PWM slider for DO channels by software toggling (duty-cycle emulation).
- This is convenient for integration tests, but it is not proof of native hardware PWM on DIO pins.
- DI read path is digital state-oriented; exact analog voltage measurement is not guaranteed by DIO API.

### 7.5 Safe commissioning checklist

1. Confirm the exact SKU (`-2G` or isolated DIO variant) in product label/BOM.
2. Confirm NPN/PNP scheme before energizing pin 20.
3. Tie commons correctly: DI_COM (pin 9), DIO_GND (pin 19/10).
4. Start from simple ON/OFF validation for each channel before automation.
