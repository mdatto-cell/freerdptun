# FreeRDPTun 0.1.3: транспорт, padding и shaping

Пакет обновляет Go-программы Linux x64 и Windows x64 установленного
FreeRDPTun 0.1.0–0.1.2. UUID, адреса, серверный сертификат и pin сохраняются.
Рабочие автонастройка TUN и ранний NLA-отказ для mstsc из 0.1.2 сохранены.

## Изменения

- **Профили направлений:** независимые `transport.upload` и `transport.download`
  с padding, fragmentation, случайным target size, jitter и исключением малых IP.
- **Контроль скорости:** общий и индивидуальный token bucket, настраиваемая
  ёмкость burst, учёт по внутренним IP или собственным DATA-фреймам с padding.
- **Серверное управление:** клиент получает эффективную политику в
  HMAC-подтверждённом ответе UUID-авторизации v2. Его JSON менять не требуется.
- **Keepalive:** настраиваемый диапазон интервалов простоя исходящей передачи.
- **Диагностика:** `POLICY`, периодические и итоговые `STATS`, собственные
  накладные расходы, ожидания limiter/jitter/native pipe, очередь и drops.
- **Документация:** `CONFIGURATION_RU.md` описывает все поля, диапазоны,
  единицы, правила нулей и ограничения; `examples/` содержит три пресета.

По умолчанию `baseline` не включает padding/дробление/jitter, но прежние
лимиты скорости продолжают действовать. Для контрольного замера без всех
встроенных limiter задайте отдельно `rate_limit_enabled: false`.
Новые настройки меняют только поток внутри настоящего FreeRDP DVC.

## Состав и зависимости

- **`dist/linux-amd64/freerdptun`:** Go-программа сервера и Linux-клиента 0.1.3.
- **`dist/windows-amd64/freerdptun.exe`:** Go-программа Windows-клиента 0.1.3.
- **`source/`:** полный собственный Go/C-код, примеры, скрипты, systemd-unit,
  документация и лицензии для наложения на исходный проект.
- **`CONFIGURATION_RU.md`:** отдельная копия полного справочника.
- **`README.md`:** эта инструкция.
- **`SHA256SUMS`:** SHA-256 файлов пакета, кроме самого списка.

Это обновление, не отдельная полная поставка FreeRDP. Сохраняются native-файлы
из первоначального полного комплекта 0.1.0: `rdpt-bridge`, FreeRDP/WinPR
DLL/SO и подписанный Wintun. В обновлении они не дублируются; закреплённые
сторонние исходники также остаются в исходном полном комплекте.
Версия native-моста остаётся 0.1.0, FreeRDP остаётся 3.28.0.

Первоначальный пакет обновления был подготовлен без публикации на GitHub.
Позднее по отдельному разрешению опубликован публичный репозиторий
[mdatto-cell/freerdptun](https://github.com/mdatto-cell/freerdptun).
Секретов и рабочих конфигураций пользователя в пакете нет;
JSON из `examples/` являются неполными пресетами.

## Совместимость и порядок

Сначала обновите сервер. Если в серверном JSON отсутствуют `transport`
и `stats_interval_seconds`, он ещё принимает старые клиенты 0.1.0–0.1.2
в прежнем режиме без новых профилей. Затем обновите клиентские Go-бинарники
и только после этого добавляйте новые серверные поля.

Новый клиент требует новый сервер; автоматического downgrade нет.
Явное наличие `transport`, даже `{}`, или `stats_interval_seconds`, даже `0`,
требует клиентов 0.1.3+. Новая политика применяется при следующем подключении.

Не выполняйте `init-server` поверх существующего state. Не заменяйте UUID,
сертификат/ключ, pin и ваши адреса 10.77.0.1/24, 10.77.0.2/24.
Локальные маршруты и NAT из вашей песочницы сохраняются.

## Обновление сервера Linux

Распакуйте архив и перейдите в каталог `freerdptun-0.1.3-update`.
Сначала сохраните конфигурацию и остановите службу:

```bash
sha256sum -c SHA256SUMS
sudo cp -an /etc/freerdptun/server.json /etc/freerdptun/server.json.before-0.1.3
sudo systemctl stop freerdptun
sudo cp -an /opt/freerdptun/freerdptun /opt/freerdptun/freerdptun.before-0.1.3
sudo install -m 755 dist/linux-amd64/freerdptun /opt/freerdptun/freerdptun
/opt/freerdptun/freerdptun version
sudo /opt/freerdptun/freerdptun check -config /etc/freerdptun/server.json
sudo systemctl start freerdptun
sudo journalctl -u freerdptun -n 60 --no-pager
```

Ожидаемая версия главной Go-программы: `FreeRDPTun 0.1.3`.
Native-мост и библиотеки не заменяйте. Linux-клиент обновляется тем же
бинарником после его остановки; используйте фактический путь установки
и клиентский JSON, а не серверный systemd-unit.

При автонастройке адрес и MTU `frdptun0` будут взяты из текущего JSON.
Default route, DNS, forwarding, NAT и firewall программа не меняет.
Чужие/вручную постоянные TUN с тем же именем автоматически не удаляются.

## Обновление Windows-клиента

Закройте старый клиент. Из корня распакованного пакета выполните PowerShell
от администратора, скорректировав путь `C:\FreeRDPTun` при необходимости:

```powershell
if (-not (Test-Path 'C:\FreeRDPTun\freerdptun.before-0.1.3.exe')) {
  Copy-Item 'C:\FreeRDPTun\freerdptun.exe' 'C:\FreeRDPTun\freerdptun.before-0.1.3.exe'
}
Copy-Item '.\dist\windows-amd64\freerdptun.exe' 'C:\FreeRDPTun\freerdptun.exe' -Force
Set-Location 'C:\FreeRDPTun'
.\freerdptun.exe version
.\freerdptun.exe check -config .\client.json
.\freerdptun.exe run -config .\client.json
```

`client.json`, `rdpt-bridge.exe`, DLL и Wintun остаются прежними.
`transport` и `stats_interval_seconds` в клиент не добавляйте.

При желании сверяйте SHA-256 Windows-бинарника с `SHA256SUMS` из архива:

```powershell
Get-FileHash 'C:\FreeRDPTun\freerdptun.exe' -Algorithm SHA256
```

## Первый эксперимент

После обновления обеих сторон перенесите поля из `examples/transport-baseline.json`
в верхний уровень своего серверного JSON. Это объект для слияния, не
полная замена конфигурации: сохраняйте все остальные настройки и allowlist.
Не создавайте два блока `transport` или два `stats_interval_seconds`.

Затем выполните `check`, перезапустите сервер и переподключите клиента.
Обе стороны должны показать `POLICY` с `mode: "baseline"` и
`rate_limit_enabled: false`; далее появятся записи `STATS`.
Проверьте отдельно, что локальные клиентские лимиты соответствуют вашим
ожиданиям; при этом выключателе встроенные limiter не используются.

Сначала измерьте upload/download без модификаций. Вторым шагом можно
попробовать `transport-padding-download.json`, третьим сравнить fragmentation.
В обоих дополнительных примерах limiter включён: сохранённые глобальные
и индивидуальные значения опять действуют. Jitter во всех примерах равен 0.

Для каждого эксперимента фиксируйте направление и условия нагрузки,
обе строки `POLICY`, финальные `STATS` и pcap. При заметном падении скорости
сначала сравнивайте `*_limiter_wait_ms`, `tx_jitter_wait_ms`,
`tx_pipe_write_ms`, `queue_drops` и долю padding.

Это не профили, откалиброванные под ваши RDP-видеодампы. Размеры и задержки
остаются экспериментальными; гарантии неотличимости от mstsc нет.

## Откат

Остановите сервер и клиентов. Восстановите старые Go-бинарники и сохранённый
серверный JSON без новых блоков. Одного отката сервера недостаточно, если
оставить клиент 0.1.3: его авторизация требует сервера 0.1.3.
Native-компоненты, UUID, сертификат и pin не менялись.

## Проверка выпуска

Выполнены `gofmt`, `go vet ./...` для Linux и Windows, сборки Linux amd64
и Windows amd64 с `CGO_ENABLED=0`. Исходники новых регрессионных тестов
включены, но тесты не запускались.

По вашему условию не запускались приложения туннеля, native-интеграции,
loopback, TUN, сетевые подключения и нагрузочные проверки. Реальные испытания
0.1.3 выполняете вы. Исторический `TEST_REPORT_RU.md` относится только к 0.1.0.
Команда `selftest` проверяет auth/DVC echo, но не прогоняет новые профили
padding/fragmentation/shaping и не заменяет такую проверку.
