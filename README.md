# DMR Hotspot

DMR-хотспот с веб-интерфейсом (OpenGD77/MMDVM-модем, BrandMeister), эхо-репитером,
голосовым помощником (STT → LLM → TTS) и локальным web UI (http://127.0.0.1:8080).

Языки: Go (сервер и CLI), Web UI — статический `internal/webui/index.html` (en/ru).

Состав:
- `dmr-gui` — web UI + управление хотспотом,
- `dmr-hotspot` — headless-хотспот,
- `dmr-diag` — диагностика железа (список COM-портов),
- `dmr-selftest` — самопроверка,
- `dmr-gui-launcher` — только для macOS: исполняемый файл `.app`-бандла.

## Скачивание

Готовые пакеты для macOS и Windows публикуются в [Releases](../../releases) —
workflow GitHub Actions собирает их автоматически (тот же `tools/package.sh`):

| Файл                                        | Платформа          | Содержимое                                             |
|---------------------------------------------|--------------------|---------------------------------------------------------|
| `DigitalVoice-DMRHotspot-<ver>-MacOS-universal.dmg`      | macOS x86_64+arm64 | `DigitalVoice - DMR Hotspot.app` (universal) + голоса piper        |
| `DigitalVoice-DMRHotspot-<ver>-Windows-amd64.zip`        | Windows amd64      | exe-ники + `install.bat`/`uninstall.bat` + голоса piper |
| `SHA256SUMS.txt`                            | —                  | контрольные суммы                                        |

Чтобы выпустить релиз: изменить число в `VERSION` (например, `1.0.1`), закоммитить
и поставить тег — `git tag v1.0.1 && git push origin main --tags`. Workflow соберёт
пакеты и приложит их к релизу с автогенерированными release notes.

## Сборка из исходников

Нужен **Go 1.25+**. Go-модуль не тянет внешних зависимостей.
```bash
# локальная платформа (на macOS вокодер через cgo — см. раздел OpenDMR)
mkdir -p bin
CGO_ENABLED=1 go build -o bin/dmr-gui      ./cmd/dmr-gui
CGO_ENABLED=1 go build -o bin/dmr-hotspot  ./cmd/dmr-hotspot
CGO_ENABLED=1 go build -o bin/dmr-diag     ./cmd/dmr-diag
CGO_ENABLED=1 go build -o bin/dmr-selftest ./cmd/dmr-selftest
```

Без вокодера (например, на Linux/Windows) сборка тоже работает — cgo-обвязка
`internal/dmr` автоматически переходит на stub-бэкенд (`opendmr_stub.go`):

```bash
CGO_ENABLED=0 go build -o bin/dmr-hotspot ./cmd/dmr-hotspot
```

### Быстрая пересборка (tools/rebuild.sh)

Скрипт пересобирает Go-бинарники в `bin/` (по умолчанию все четыре: `dmr-gui`,
`dmr-hotspot`, `dmr-diag`, `dmr-selftest`). CGO включён по умолчанию, как в ручной
сборке; саму библиотеку вокодера он не пересобирает — при необходимости сначала
запустите `tools/build-opendmr.sh`.

```bash
tools/rebuild.sh                   # все бинарники -> bin/
tools/rebuild.sh dmr-hotspot       # только указанные: один или несколько через пробел
CGO_ENABLED=0 tools/rebuild.sh     # без вокодера (Linux/Windows, stub-бэкенд)
```

Проверка:

```bash
go test ./...
./bin/dmr-diag                      # список COM-портов
./bin/dmr-gui -no-start -no-browser # web UI без автостарта хотспота
```

### OpenDMR (вокодер AMBE)

`internal/dmr/libopendmr.dylib` — универсальная (arm64+x86_64) библиотека, собирается
из OpenDMR с патчем `tools/opendmr/opendmr-fixes.patch`. `tools/package.sh` делает это
автоматически (клон `https://github.com/MW0MWZ/OpenDMR` в `.cache/OpenDMR`,
checkout фиксированного ревизиона, `make libopendmr.dylib` для обеих архитектур, lipo).

Переменные окружения (все опциональны):

| Переменная      | По умолчанию                    | Смысл                                   |
|-----------------|---------------------------------|-----------------------------------------|
| `OPENDMR_SRC`   | `<repo>/.cache/OpenDMR`         | каталог исходников OpenDMR              |
| `OPENDMR_REPO`  | `https://github.com/MW0MWZ/OpenDMR.git` | git-репозиторий                  |
| `OPENDMR_REV`   | `d28164b…`                      | фиксированный коммит OpenDMR            |

## Сборка установочных пакетов

Один скрипт собирает оба дистрибутива:

```bash
bash tools/package.sh
```

Артефакты в `dist/`:

| Файл                                        | Платформа          | Содержимое                                             |
|---------------------------------------------|--------------------|---------------------------------------------------------|
| `DigitalVoice-DMRHotspot-<ver>-MacOS-universal.dmg`      | macOS x86_64+arm64 | `DigitalVoice - DMR Hotspot.app` (universal) + голоса piper        |
| `DigitalVoice-DMRHotspot-<ver>-Windows-amd64.zip`        | Windows amd64      | exe-ники + `install.bat`/`uninstall.bat` + голоса piper |
| `SHA256SUMS.txt`                            | —                  | контрольные суммы                                        |

Номер версии берётся из файла `VERSION` в корне репозитория — это единственное место,
которое нужно менять при выпуске новой версии. Первый релиз — `1.0.0`; имя пакета
всегда содержит этот номер (например, `DigitalVoice-DMRHotspot-1.0.0-Windows-amd64.zip`).

Переменные окружения:

| Переменная       | По умолчанию      | Смысл                                            |
|------------------|-------------------|--------------------------------------------------|
| `INCLUDE_VOICES` | `1`               | `0` — не класть ~241 МБ голосов piper в пакеты   |
| `VERSION`        | из файла `VERSION`| строка версии (в именах пакетов, Info.plist)     |

Пример:

```bash
INCLUDE_VOICES=0 VERSION=1.2.0 bash tools/package.sh
```

Локальная упаковка (без GitHub): переписать число в `VERSION` (например, `1.0.1`),
запустить `tools/package.sh` — новые пакеты появятся в `dist/` со своим номером,
а устаревшие можно удалить вручную или оставить (`SHA256SUMS.txt` покрывает все
пакеты в `dist/`). Для публикации релиза на GitHub достаточно тега `v*` —
см. раздел «Скачивание».

Порядок работы скрипта:

1. `tools/genicon` рисует иконку (см. ниже) и строит `app.ico`;
2. `sips`+`iconutil` собирают `app.icns` из master-PNG;
3. OpenDMR: клон/checkout/patch, `make` для arm64 и x86_64, `lipo` → `libopendmr.dylib`;
4. Go-бинарники для darwin arm64+amd64 (CGO: вокодер), `lipo` в universal; для Windows — CGO_ENABLED=0;
5. RPATH-фиксы и подпись ad-hoc (`codesign`) на macOS;
6. Сборка `DigitalVoice - DMR Hotspot.app` (Info.plist с `CFBundleIconFile=app.icns`), `hdiutil create` → DMG;
7. Windows-набор: exe + `app.ico`/`icon.png` + `install.bat` (ярлыки с иконкой `app.ico`) → ZIP;
8. `SHA256SUMS.txt`, очистка временного каталога `dist/.build`.

Требования к среде: macOS (для `hdiutil`/`iconutil`/`codesign`/`lipo`), Xcode Command
Line Tools (для кросс-сборки amd64 нужен clang), установленный Go.

## Иконка приложения

Исходник генератора — `tools/genicon/main.go` (чистый Go stdlib): рисует цифровой
DMR-сигнал — 4FSK-бёрст из 28 столбцов с четырьмя уровнями символов, два слота TDMA
и пиксельную надпись «DMR». Рендер в 4x с бокс-фильтром для антиалиасинга.

```bash
# master-PNG для web UI (favicon) и заготовка для icns:
go run ./tools/genicon
# отдельно ico для Windows:
go run ./tools/genicon -png 0 -ico app.ico
# произвольный размер:
go run ./tools/genicon -png icon-256.png -size 256
```

Где иконка используется:

- `internal/webui/icon.png` — встроена в бинарник и раздаётся `/icon.png` (favicon web UI);
- `tools/package.sh` — из этого же PNG строит `app.icns` для `DigitalVoice - DMR Hotspot.app`
  (`CFBundleIconFile` в Info.plist) и `app.ico` для Windows-комплекта (ярлыки
  на рабочем столе и в меню «Пуск»).

## Установка

- **macOS**: смонтировать DMG, перетащить `DigitalVoice - DMR Hotspot.app` в Applications;
  конфиг появится при первом запуске (шаблон — `config.example.yaml`).
- **Windows**: распаковать zip, `install.bat` (правой кнопкой → «Установить»);
  ярлыки появятся на рабочем столе и в меню «Пуск», файлы — в
  `%LOCALAPPDATA%\Programs\DigitalVoice - DMR Hotspot`. Удаление — `uninstall.bat`.
  Подписи нет: SmartScreen может предупредить — «Подробнее» → «Выполнить в любом случае».

Ограничения Windows-версии (состояние кода): передача через MMDVM-модем по COM-порту
и вокодер (libopendmr) пока доступны только на macOS; web UI, редактор конфига и
логика подключения к BrandMeister работают.

## Структура

```
cmd/                 точки входа (dmr-gui, dmr-hotspot, dmr-diag, dmr-selftest, dmr-gui-launcher)
internal/webui/      web UI (index.html + встроенная иконка + HTTP API)
internal/core/       контроллер режима, эхо, агент (STT/LLM/TTS)
internal/dmr/        DMR-стек + cgo-обвязка OpenDMR (libopendmr.dylib)
internal/modem/      последовательный порт MMDVM (darwin/linux; Windows — заглушка)
internal/network/    BrandMeister HomeBrew-клиент
internal/config/     конфиг (YAML), провайдеры LLM
tools/package.sh     сборка DMG и ZIP (установочные пакеты, имена с номером версии)
VERSION              текущий номер версии (первый релиз — 1.0.0, растёт со временем)
tools/genicon/       генератор иконки приложения
tools/opendmr/       патч для сборки OpenDMR
voices/piper/        голоса TTS (в git не хранятся, но кладутся в пакеты)
```
