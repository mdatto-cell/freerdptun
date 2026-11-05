# FreeRDPTun 0.1.3: установка и испытание

Инструкция относится только к новому проекту FreeRDP–FreeRDP. Старые программы,
UUID и конфигурации RDPTun 0.4.0 с ним несовместимы; миграция не предусмотрена.

Если у вас уже установлен FreeRDPTun 0.1.0–0.1.2, используйте
`docs/UPDATE_0.1.3_RU.md` и сохраните существующий state. Повторный `init-server`
не требуется. Архив обновления содержит только новые Go-бинарники; нативные
файлы и сторонние библиотеки берутся из полного комплекта 0.1.0 или 0.1.3.
Полный справочник: `docs/CONFIGURATION_RU.md`. Сервер обновляется первым;
для новых профилей нужны клиенты 0.1.3.

## Подготовка

Сервер: Ubuntu 22.04 x64 с доступным TCP-портом 3389. Клиенты: Linux x64
либо Windows 10/11 x64 с правами администратора для создания TUN/Wintun.
Готовый Linux-комплект рассчитан на Ubuntu 22.04 и совместимые дистрибутивы
с glibc/OpenSSL 3; для Alpine/musl он не предназначен.

Убедитесь, что порт не занят прежним RDPTun, xrdp или другой службой.
Останавливайте их только если они действительно работают на этой тестовой машине.

```bash
sudo ss -lntp '( sport = :3389 )'
```

На Linux установите системные библиотеки и инструменты:

```bash
sudo apt update
sudo apt install -y libssl3 zlib1g libzstd1 iproute2
sudo modprobe tun
ls -l /dev/net/tun
```

FreeRDP и WinPR поставляются рядом с программой в `lib/`; системный пакет
`freerdp2` или `xrdp` не требуется. Не заменяйте поставляемые библиотеки случайными
DLL/SO другой версии и не добавляйте глобальный `LD_LIBRARY_PATH`.

## Установка серверных файлов

Распакуйте архив, перейдите в его корневой каталог и выполните:

```bash
sudo install -d -m 755 /opt/freerdptun
sudo cp -a dist/linux-amd64/. /opt/freerdptun/
sudo chown -R root:root /opt/freerdptun
sudo chmod 755 /opt/freerdptun/freerdptun /opt/freerdptun/rdpt-bridge
/opt/freerdptun/freerdptun version
/opt/freerdptun/rdpt-bridge --version
```

Подкаталог `/opt/freerdptun/lib` обязателен. Он содержит библиотеки, которые
нативный транспорт находит через относительный RPATH `$ORIGIN/lib`.

## Новый сервер, сертификат и первый клиент

Для `-dir` укажите новый, ещё не существующий каталог. В `-server` задайте
публичный адрес или DNS-имя сервера с портом для клиентского шаблона.

```bash
sudo /opt/freerdptun/freerdptun init-server \
  -dir /etc/freerdptun \
  -server vpn.example.net:3389

sudo /opt/freerdptun/freerdptun check -config /etc/freerdptun/server.json
sudo /opt/freerdptun/freerdptun selftest -config /etc/freerdptun/server.json
```

Будут созданы четыре файла:

- **`server.json`:** серверная конфигурация со списком клиентов.
- **`client.json`:** готовая конфигурация первого клиента с UUID и pin сервера.
- **`server.crt`:** новый самоподписанный серверный TLS-сертификат сроком на год.
- **`server.key`:** секретный ключ сертификата; никогда не передавайте его клиентам.

Сертификат создаётся автоматически; покупать или регистрировать его не нужно.
Клиент не использует собственный сертификат, но обязан иметь верный SHA-256 pin
серверного сертификата, который генерируется в `client.json`.

Передайте `client.json` клиенту защищённым способом. Он содержит секрет доступа,
поэтому не публикуйте его и не отправляйте в открытые журналы или общедоступные чаты.

`selftest` использует только loopback TCP, настоящий FreeRDP/DVC и обмен
тестовыми сообщениями. Он не создаёт TUN, не меняет сеть и не занимает внешний 3389;
успех не означает, что ваши маршруты или внешнее подключение уже проверены.

## Конфигурация сервера

Основные поля уже созданы генератором. Изменять их можно обычным редактором,
после чего следует выполнить `check` и перезапустить сервер.

```json
{
  "mode": "server",
  "address": "0.0.0.0:3389",
  "tun": "frdptun0",
  "cidr": "10.78.0.1/24",
  "mtu": 1400,
  "certificate": "server.crt",
  "private_key": "server.key",
  "total_upload_bps": 100000000,
  "total_download_bps": 100000000,
  "max_connections": 32,
  "handshake_timeout_seconds": 20,
  "idle_timeout_seconds": 60,
  "queue_packets": 256,
  "auto_configure_tun": true,
  "stats_interval_seconds": 10,
  "transport": {
    "mode": "baseline",
    "rate_limit_enabled": true,
    "rate_accounting": "ip",
    "burst_bytes": 65536,
    "keepalive_min_seconds": 10,
    "keepalive_max_seconds": 10,
    "reassembly_timeout_seconds": 30,
    "upload": {},
    "download": {}
  },
  "rdp_decoy": {
    "enabled": true,
    "handshake_timeout_seconds": 10,
    "max_connections": 16
  },
  "clients": [
    {
      "id": "test-01",
      "uuid": "ЗДЕСЬ_НОВЫЙ_UUID_ИЗ_ГЕНЕРАТОРА",
      "ipv4": "10.78.0.2",
      "upload_bps": 0,
      "download_bps": 0
    }
  ]
}
```

Это иллюстрация полей, а не конфигурация с готовым секретом. Используйте
реальный `server.json`, созданный `init-server`, и сохраняйте его закрытым.

- **`address`:** адрес прослушивания; для теста стандартно TCP 3389.
- **`tun`:** новое имя интерфейса, не занятое другим сервисом.
- **`cidr`:** адрес серверного TUN и сеть допустимых клиентских IPv4;
  начиная с 0.1.2 адрес назначается автоматически.
- **`mtu`:** одинаковый MTU на сервере, клиентах и интерфейсах; начальное значение 1400.
- **`certificate`, `private_key`:** пути относительно JSON или абсолютные пути.
- **`bridge`:** необязательный путь к нативному процессу; по умолчанию
  `rdpt-bridge` рядом с основным исполняемым файлом.
- **`total_upload_bps`:** суммарный лимит от клиентов к серверу, бит/с.
- **`total_download_bps`:** суммарный лимит от сервера к клиентам, бит/с.
- **`upload_bps`, `download_bps`:** дополнительные ограничения конкретного клиента,
  направления те же; итог ограничен и индивидуальным, и суммарным лимитами.
- **`max_connections`:** максимум одновременно принятых соединений, включая
  ещё не авторизованные. Значение от 1 до 256.
- **`handshake_timeout_seconds`:** общий срок RDP, TLS, DVC и UUID-авторизации,
  от 3 до 120 секунд.
- **`idle_timeout_seconds`:** срок без входящих прикладных сообщений, от 15
  до 3600 секунд; новый сеанс использует серверное значение на обеих сторонах.
- **`queue_packets`:** ограничение исходящей очереди одного сеанса, 16..4096
  элементов. При заполнении серверная очередь отбрасывает новые IP-пакеты.
- **`auto_configure_tun`:** автоматически назначать адрес, MTU и включать link;
  по умолчанию `true`, в том числе для старых JSON без этого поля.
- **`rdp_decoy`:** серверная ветка раннего NLA-отказа. Если блока нет, включена:
  срок 10 секунд, максимум 16 одновременных отказных сеансов; они также входят
  в общий `max_connections`. `{"enabled": false}` отключает эту ветку.

По умолчанию лимиты учитывают полную внутреннюю IP-длину, не весь внешний
трафик TCP/TLS/RDP. `rate_accounting: "frame"` включает padding и наши
заголовки DATA. Нулевой лимит отключает только свой предел; независимый
`rate_limit_enabled: false` отключает все встроенные limiter нового сеанса.

В `baseline` padding, fragmentation и jitter выключены; в `profiled`
настраиваются отдельно для upload/download. Пустые профили в этом примере
дают модификаторы `false`, jitter 0 и размеры 1200..1800, которые без
модификаторов на DATA не влияют. Полные примеры находятся в `examples/`.

Каждый limiter использует token bucket с настраиваемым запасом
`burst_bytes`, по умолчанию 64 KiB. Короткий всплеск может превышать
заданную мгновенную скорость; накопленные токены компенсируют запоздания
таймера. Подробности, ограничения и счётчики: `CONFIGURATION_RU.md`.

## Первый запуск сервера

Сначала запустите сервер в консоли и оставьте её открытой:

```bash
sudo /opt/freerdptun/freerdptun run -config /etc/freerdptun/server.json
```

Адрес, MTU и link уже настроены программой. Во втором терминале проверьте:

```bash
ip address show dev frdptun0
```

IP forwarding, NAT, firewall, default route и разрешённые сети настройте
самостоятельно. ОС создаёт маршрут подключенной подсети при назначении IP.
Интерфейс непостоянный и исчезает при остановке приложения; при новом запуске
программа повторяет создание и настройку. При ошибке настройки запуск
завершается, а созданный интерфейс закрывается.

## Запуск Linux-клиента

Скопируйте весь каталог `dist/linux-amd64` на клиент, например в `/opt/freerdptun`,
и переданный `client.json` в закрытый каталог. Установите те же системные
библиотеки, что указаны в разделе подготовки.

Проверьте в клиентском JSON:

- **`address`:** действительный внешний адрес сервера и порт.
- **`uuid`:** новый UUID, присутствующий в серверном списке.
- **`ipv4`:** выделенный именно этому UUID адрес, например `10.78.0.2`.
- **`cidr`:** этот же адрес с префиксом, например `10.78.0.2/24`.
- **`server_sha256`:** автоматически выданные 64 hex-символа SHA-256.
- **`mtu`:** тот же MTU, что на сервере.

```bash
sudo /opt/freerdptun/freerdptun check -config /etc/freerdptun/client.json
sudo /opt/freerdptun/freerdptun run -config /etc/freerdptun/client.json
```

Во втором терминале:

```bash
ip address show dev frdptun0
ping -c 3 10.78.0.1
```

Для первого испытания не меняйте default route. Сначала проверьте адрес
сервера внутри туннеля и только затем добавляйте нужные маршруты.

## Запуск Windows-клиента

Скопируйте весь каталог `dist/windows-amd64`, например в
`C:\FreeRDPTun`, и поместите рядом `client.json`.
Не копируйте отдельно только EXE: нужны поставляемые DLL, включая `wintun.dll`.

Откройте PowerShell от имени администратора:

```powershell
cd C:\FreeRDPTun
.\freerdptun.exe check -config .\client.json
.\freerdptun.exe run -config .\client.json
```

Во втором PowerShell от администратора после создания интерфейса:

```powershell
Get-NetIPAddress -InterfaceAlias "frdptun0"
Get-NetIPInterface -InterfaceAlias "frdptun0" -AddressFamily IPv4
ping 10.78.0.1
```

Подставьте свой адрес и имя интерфейса из JSON. Остановка: `Ctrl+C` в первом
окне; автоматического переподключения в этой версии нет.

Назначение выполняется через Windows PowerShell и модуль NetTCPIP, только для
созданного Wintun: DHCP выключается, APIPA заменяется адресом из JSON, MTU
применяется к IPv4. Программа ожидает `Preferred`, не отключая DAD.
Windows не считает новый адрес пригодным до завершения DAD, если эта проверка
включена ([Microsoft New-NetIPAddress](https://learn.microsoft.com/en-us/powershell/module/nettcpip/new-netipaddress?view=windowsserver2025-ps)).

Wintun поставляется в оригинальной подписанной amd64-версии 0.14.1, без
модификации DLL; сведения и исходный комплект доступны на
[официальном сайте Wintun](https://www.wintun.net/).
Наши EXE не имеют Authenticode-подписи, поэтому Windows может показать
предупреждение для скачанного приложения.

## Добавление второго клиента

Сгенерируйте новый UUID и добавьте отдельную запись с новым IPv4 на сервере:

```bash
/opt/freerdptun/freerdptun new-uuid
```

Второму клиенту, например, выделите `10.78.0.3`. Создайте для него копию
клиентского JSON и измените `uuid`, `ipv4`, `cidr`; pin сервера остаётся прежним.

`id`, UUID и IPv4 в списке должны быть уникальными. Одновременно разрешён
только один сеанс на UUID; второй такой сеанс отклоняется и не вытесняет первый.
Для секрета используйте `new-uuid`, а не короткие или предсказуемые значения,
записанные в формате UUID.

Горячая перезагрузка конфигурации пока отсутствует. Для применения списка
клиентов или лимитов требуется перезапуск сервера, который отключит все сеансы.

## Systemd

После успешного консольного испытания установите пример службы:

```bash
sudo install -m 644 packaging/freerdptun.service /etc/systemd/system/freerdptun.service
sudo systemctl daemon-reload
sudo systemctl enable --now freerdptun
sudo journalctl -u freerdptun -f
```

Перед запуском службы остановите консольный экземпляр. Начиная с 0.1.2,
приложение внутри службы само назначает адрес/MTU/link. Команды `ExecStartPost`
для повторного назначения адреса не нужны; настройка NAT/forwarding остаётся вашей.

Проверка и остановка:

```bash
sudo systemctl status freerdptun
sudo systemctl stop freerdptun
```

## Диагностика

- **`server certificate pin mismatch`:** неверный `server_sha256`, другой
  сервер либо заменённый сертификат. Не отключайте проверку; безопасно
  сверьте новый pin командой `fingerprint -cert /etc/freerdptun/server.crt`.
- **`authentication refused`:** неизвестный UUID, неверный proof или IPv4
  не соответствует разрешённому адресу.
- **`MTU mismatch`:** выставьте одинаковые `mtu` на обеих сторонах и интерфейсах.
- **`UUID already connected` / `refused duplicate`:** уже есть сеанс этого UUID;
  для второго устройства выдайте отдельный UUID и адрес.
- **`native bridge` / ошибка DLL/SO:** рядом с Go-программой нет нативного
  процесса либо потеряны библиотеки из комплекта.
- **`TUN name exists`:** имя занято; остановите старый экземпляр или выберите
  другое имя. Программа намеренно не подключается к чужому интерфейсу.
- **`create Wintun adapter`:** проверьте запуск от администратора, наличие
  `wintun.dll` и конфликт существующего имени.
- **Подключение есть, ping не идёт:** проверьте строку автоматического назначения адреса,
  link up, одинаковый MTU, Windows firewall и локальные маршруты.
- **`automatic TUN setup`:** проверьте `iproute2` и права root на Linux;
  запуск от администратора, Windows PowerShell/NetTCPIP и состояние адреса на Windows.
- **`RDP NLA refusal sent ... STATUS_LOGON_FAILURE`:** ожидаемый отказ NLA-клиенту,
  не ошибка UUID-сеанса. Нельзя использовать `mstsc` как VPN-клиент.
- **Туннель до сервера работает, интернета нет:** маршрутизация, NAT, forwarding
  и DNS не настраиваются приложением и проверяются отдельно.

Сообщения FreeRDP об отключённых AAD/NLA-функциях сами по себе не означают отказ
UUID-аутентификации. Полезные признаки: `RDP session activated`, затем
`DVC connected` и строка Go-программы `connected`.

## Сборка исходников на Ubuntu 22.04

Готовые файлы уже находятся в `dist`. Для самостоятельной сборки установите
Go 1.23 или новее отдельно, затем:

```bash
sudo apt install -y build-essential cmake ninja-build libssl-dev pkg-config
bash scripts/build-linux.sh
go test -race ./...
go vet ./...
```

Скрипт использует закреплённые исходники `third_party/FreeRDP` и не обращается
к GitHub. Windows-сборка выполняется на Linux через MinGW-w64; подробности
находятся в `docs/BUILD_RU.md`.
