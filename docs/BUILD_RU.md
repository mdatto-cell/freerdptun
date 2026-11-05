# FreeRDPTun: сборка из исходников

В готовых архивах релиза Linux/Windows файлы находятся в `dist`; в Git
бинарников нет. Исходники основной зависимости FreeRDP 3.28.0 включены
в `third_party/FreeRDP`; скрипты сборки не публикуют файлы и не создают
коммиты или GitHub-репозитории.

Для update-пакета 0.1.3 сторонние файлы и закреплённые исходники FreeRDP
нужно взять из полного комплекта 0.1.0. Собственный Go/C-код включён
полностью. В 0.1.3 native не менялся; пересобирать его для обновления
Go-программы не требуется.

## Сборка только Go-части обновления

```bash
gofmt -w cmd internal
go vet ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' \
  -o dist/linux-amd64/freerdptun ./cmd/freerdptun
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w' \
  -o dist/windows-amd64/freerdptun.exe ./cmd/freerdptun
```

При подготовке 0.1.3 выполнены только компиляция, форматирование и статический
анализ. Следующие ниже команды тестов приведены для вашей песочницы;
нами не запускались тесты, приложения, loopback, TUN или сетевые соединения.
`selftest` проверяет auth/DVC echo, но не новые транспортные модификаторы.

## Linux x64

Для целевой совместимости выполняйте сборку на Ubuntu 22.04 x64.
Бинарник нативного транспорта, собранный на более новой glibc, не считается
автоматически совместимым с Ubuntu 22.04.

```bash
sudo apt update
sudo apt install -y build-essential cmake ninja-build libssl-dev pkg-config
# Установите Go 1.23+ отдельно.
bash scripts/build-linux.sh
go test -race ./...
go vet ./...
```

Результат: `dist/linux-amd64`. Go-программа собирается с `CGO_ENABLED=0`;
нативный процесс динамически связывается с поставляемыми FreeRDP/WinPR
и системными OpenSSL/zlib/zstd.

## Windows x64: кросс-сборка на Linux

Используются MinGW-w64, Go и статические Windows-библиотеки OpenSSL.
Для релизного комплекта использован OpenSSL 3.5.9; исходный архив и его SHA-256
указаны в `THIRD_PARTY.md`.

```bash
sudo apt install -y build-essential cmake ninja-build gcc-mingw-w64-x86-64 \
  g++-mingw-w64-x86-64 perl

# Распакуйте проверенные исходники OpenSSL 3.5.9 в отдельный каталог:
cd /path/to/openssl-3.5.9
./Configure mingw64 --cross-compile-prefix=x86_64-w64-mingw32- \
  no-shared no-tests no-module --prefix=/absolute/path/openssl-win
make -j4
make install_sw

cd /path/to/freerdp-tun
OPENSSL_WIN=/absolute/path/openssl-win bash scripts/build-windows-cross.sh
```

После сборки скопируйте оригинальную подписанную `wintun.dll` из `wintun/bin/amd64`
официального комплекта Wintun 0.14.1 в `dist/windows-amd64`.
Не изменяйте и не пропускайте через `strip` эту подписанную DLL.

OpenSSL исходники отдельно в архив приложения не включены, потому что
используется Apache-2.0 компонент со статической линковкой и приложенными
уведомлениями/лицензией; ссылка на точный исходный архив дана в `THIRD_PARTY.md`.
Полный собственный код и закреплённые исходники FreeRDP находятся в комплекте.

## Тесты

Обычные Go-тесты не требуют TUN и не создают интерфейсы:

```bash
go test -race ./...
go vet ./...
go test ./internal/tunnel -run '^$' -fuzz FuzzFrameRead -fuzztime=10s
go test ./internal/tunnel -run '^$' -fuzz FuzzIPv4 -fuzztime=10s
```

Для интеграционных проверок настоящего FreeRDP создайте новый временный state,
затем явно передайте пути. Тесты используют loopback, память вместо TUN и не
меняют маршруты или firewall.

```bash
dist/linux-amd64/freerdptun init-server -dir /tmp/frdptun-new-test
RDPT_SERVER_CONFIG=/tmp/frdptun-new-test/server.json \
RDPT_BRIDGE="$PWD/dist/linux-amd64/rdpt-bridge" \
  go test -race ./internal/tunnel -run TestNative -v -count=1
```

При отсутствии `RDPT_SERVER_CONFIG` native-интеграционные тесты пропускаются.
Обычный `go test` без этой переменной сам по себе не проверяет совместимость
нормального RDP/DVC-соединения.
