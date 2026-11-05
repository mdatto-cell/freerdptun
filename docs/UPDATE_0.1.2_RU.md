# FreeRDPTun 0.1.2: автонастройка TUN и отказ mstsc

Обновление для установленного FreeRDPTun 0.1.0/0.1.1: Ubuntu/Linux x64 сервер
и клиенты Linux/Windows x64. UUID, адреса, сертификат, ключ и certificate pin
сохраняются; повторно выполнять `init-server` не нужно.

## Что изменено

- **Linux, сервер и клиент:** программа создаёт свой TUN, назначает адрес
  из `cidr`, применяет `mtu` и включает link. Требуются root/CAP_NET_ADMIN
  и установленный `iproute2`.
- **Windows-клиент:** после создания Wintun программа через Windows PowerShell
  и NetTCPIP назначает IPv4/префикс из JSON, применяет MTU, отключает DHCP
  и убирает посторонние IPv4 только на своём новом адаптере. Параметры передаются
  как данные, не как фрагменты PowerShell-кода; операции выполняются по
  индексу выбранного адаптера. Требуется запуск от администратора.
- **Готовность Windows-адреса:** клиент ожидает `Preferred` перед подключением;
  `Duplicate`, `Invalid` или превышение срока приводят к ошибке запуска.
  Общий срок PowerShell-настройки ограничен 45 секундами, ожидание состояния
  адреса после назначения ограничено 20 секундами.
- **Ошибка настройки:** запуск не продолжается с ненастроенным TUN.
  Собственный созданный интерфейс закрывается.
- **Серверный `mstsc`-отказ:** на том же TCP-порту восстановлена отдельная ветка
  X.224 → TLS → ранний отказ CredSSP/NLA с `STATUS_LOGON_FAILURE`.
  Успешного Windows-входа и рабочего стола нет, любые учётные данные отклоняются.
- **Рабочий туннель:** наш FreeRDP-клиент предлагает TLS-only; его TCP-поток
  передаётся прежнему нативному `rdpt-bridge` без изъятия начального пакета.
  UUID-авторизация внутри DVC и исправление multicast/broadcast из 0.1.1 сохранены.

Default route, DNS, NAT, forwarding и firewall программа не настраивает.
Маршрут подключенной подсети автоматически появляется в ОС после назначения
адреса. Ручные действия на других интерфейсах из прежних экспериментов это
обновление не отменяет.

## Конфигурации менять не обязательно

Обе функции по умолчанию включены даже для существующих JSON, в которых нет
новых полей. Ваша адресация `10.77.0.1/24` и `10.77.0.2/24` будет взята
из текущих `cidr`; шаблонные `10.78.0.*` из `init-server` её не заменяют.

Если хотите явно закрепить настройки, добавьте в серверный JSON верхнего уровня:

```json
"auto_configure_tun": true,
"rdp_decoy": {
  "enabled": true,
  "handshake_timeout_seconds": 10,
  "max_connections": 16
}
```

Это фрагмент объекта, не отдельный валидный JSON-файл. В клиентском JSON можно
явно добавить только `"auto_configure_tun": true`; `rdp_decoy` относится
исключительно к серверу и клиентским валидатором отклоняется.

Для внешнего сетевого менеджера допускается `"auto_configure_tun": false`:
интерфейс создаётся, но адрес и MTU остаются вашей задачей.
Блок `"rdp_decoy": {"enabled": false}` отключает ранний NLA-отказ.
Таймаут decoy допускает 3..120 секунд, лимит 1..256; отказные сеансы также
занимают слоты общего серверного `max_connections`.

## Состав архива обновления

- **`dist/linux-amd64/freerdptun`:** новая Go-программа сервера и Linux-клиента.
- **`dist/windows-amd64/freerdptun.exe`:** новая Go-программа Windows-клиента.
- **`source/`:** собственные Go/C-исходники, сценарии сборки, systemd-пример,
  документация и лицензии, для наложения на полный исходный проект 0.1.0.
- **`README.md`:** эта инструкция.
- **`SHA256SUMS`:** SHA-256 всех файлов пакета, кроме самого списка.

Нативные `rdpt-bridge`, DLL/SO, Wintun и исходники стороннего FreeRDP не
дублируются в обновлении: они остаются из полного комплекта 0.1.0.
Версия Go-приложения станет 0.1.2; версия `rdpt-bridge` останется 0.1.0,
это ожидаемо. Само обновление с GitHub не связывается и ничего не публикует.

## Обновление Linux-сервера

Распакуйте пакет и перейдите в каталог `freerdptun-0.1.2-update`.
Остановите службу перед заменой исполняемого файла:

```bash
sha256sum -c SHA256SUMS
sudo systemctl stop freerdptun
sudo cp -an /opt/freerdptun/freerdptun /opt/freerdptun/freerdptun.before-0.1.2
sudo install -m 755 dist/linux-amd64/freerdptun /opt/freerdptun/freerdptun
/opt/freerdptun/freerdptun version
sudo /opt/freerdptun/freerdptun check -config /etc/freerdptun/server.json
sudo systemctl start freerdptun
sudo journalctl -u freerdptun -n 50 --no-pager
ip -4 address show dev frdptun0
ip link show dev frdptun0
```

Ожидаемая версия: `FreeRDPTun 0.1.2`. Ожидаемая строка при вашей адресации:

```text
TUN frdptun0 configured automatically: address=10.77.0.1/24 MTU=1400
```

Ручные `ip address add` и `ip link set ... up` после старта больше не нужны.
Systemd-unit менять не требуется. Если вы добавляли собственный `ExecStartPost`
для назначения того же адреса, уберите дублирующую настройку либо отключите
`auto_configure_tun`, иначе старый скрипт может получить `File exists`.

Программа по-прежнему отказывается захватывать уже существующий Linux-интерфейс.
Обычный непостоянный TUN исчезает при остановке старого процесса. Если вы
ранее создали отдельный постоянный TUN вручную и после останова он остался,
проверьте его владельца/назначение и освободите имя только если это ваш
тестовый интерфейс. Чужие интерфейсы программа автоматически не удаляет.

Для Linux-клиента замените тот же `dist/linux-amd64/freerdptun`,
затем запустите с сохранённым `client.json` от root. MTU и адрес применятся
автоматически; собственные правила NAT/маршрутизации не меняйте ради обновления.

## Обновление Windows-клиента

Сначала остановите клиент через `Ctrl+C`. Сохраните старый
`C:\FreeRDPTun\freerdptun.exe`, затем скопируйте новый
`dist\windows-amd64\freerdptun.exe` из архива поверх него.
Не заменяйте `client.json`, `rdpt-bridge.exe`, DLL или Wintun.

В PowerShell от имени администратора:

```powershell
cd C:\FreeRDPTun
.\freerdptun.exe version
.\freerdptun.exe check -config .\client.json
.\freerdptun.exe run -config .\client.json
```

Во втором окне можно проверить:

```powershell
Get-NetIPAddress -InterfaceAlias "frdptun0" -AddressFamily IPv4
Get-NetIPInterface -InterfaceAlias "frdptun0" -AddressFamily IPv4
Find-NetRoute -RemoteIPAddress 10.77.0.1
ping -n 4 10.77.0.1
```

Ожидается IPv4 `10.77.0.2`, префикс 24, состояние `Preferred`, MTU 1400
и маршрут к `10.77.0.1` через TUN. Ручной `New-NetIPAddress` не требуется.
Для нового IPv4 Windows с включённым DAD ожидает завершения проверки уникальности
перед использованием адреса ([Microsoft New-NetIPAddress](https://learn.microsoft.com/en-us/powershell/module/nettcpip/new-netipaddress?view=windowsserver2025-ps)).
Проверки и ping в этом разделе выполняете вы, они не запускались при подготовке.

## Что ожидать от mstsc

Запускайте обычный `mstsc` с NLA и подключайтесь к вашему серверу на TCP 3389.
Если клиент предлагает HYBRID/HYBRID_EX, сервер выбирает NLA, устанавливает
TLS существующим сертификатом и отправляет ранний отказ после первого TSRequest.
В журнале успешная отправка выглядит так:

```text
RDP NLA refusal sent peer=... status=STATUS_LOGON_FAILURE
```

До отказа `mstsc` может попросить подтвердить самоподписанный сертификат.
Вид и момент окна учётных данных зависят от версии/политик `mstsc`
и сохранённых записей; гарантировать конкретное окно на всех Windows нельзя.
Не используйте настоящие рабочие пароли для этого теста.

Это не полноценная реализация CredSSP/Windows-входа: сервер не выдаёт NTLM
challenge, не расшифровывает делегированные пароли и не сохраняет токены,
имена пользователей или пароли. Содержимое клиентских TSRequest не выводится
в журнал. Поле errorCode и версии 3/4/6 описаны в
[Microsoft TSRequest](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-cssp/6aac4dea-08ef-47a6-8747-22ea7f6d8685).

CredSSP v2 закрывается без errorCode; для v5 выбирается v4.
Если `mstsc` настроен на TLS-only без NLA, он пойдёт в обычный FreeRDP-обработчик,
а не в эту отказную ветку. Она не создаёт рабочий стол и не предоставляет
VPN-доступ без UUID. Если отказная ветка не работает как ожидается, пришлите
её строку журнала и версию Windows, без UUID и закрытых ключей.

## Граница выполненных проверок

Для 0.1.2 выполнены только сборка и статический анализ Go-кода для двух платформ:

```bash
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/linux-amd64/freerdptun ./cmd/freerdptun
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/windows-amd64/freerdptun.exe ./cmd/freerdptun
```

Добавлены исходники регрессионных тестов для разбора X.224, CredSSP errorCode
и значений по умолчанию; они не запускались. Сервер, клиент, `selftest`,
PowerShell-скрипт, TUN/Wintun и реальные/loopback-соединения для этой версии
не запускались. Исторический `TEST_REPORT_RU.md` относится только к 0.1.0.
Проверьте обе функции в своей песочнице; совместимость `mstsc` и прохождение
трафика для 0.1.2 ещё не подтверждены натурным испытанием.

## Откат

Остановите сервер/клиент, верните сохранённые Go-бинарники предыдущей версии
и запустите с прежними JSON. Если вы явно добавили новые поля, перед запуском
0.1.0/0.1.1 уберите `auto_configure_tun` и `rdp_decoy`: старый валидатор
не принимает неизвестные поля. Снова потребуется ручное назначение адреса/MTU.
UUID, сертификат, ключ, pin и нативный транспорт менять для отката не нужно.
