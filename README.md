# Remnanode (Go)

Высокопроизводительный, легковесный Xray-core для панелей **Remnawave** и **Gowave**, написанный на **100% чистом Go**.

Remnanode (Go) — это полноценная замена официальной Node.js-ноды. больше не нужны тяжелые Node.js и зависимости: один компактный бинарник потребляет всего **~10–15 МБ RAM** и готов работать даже на слабых VPS, микро-инстансах и роутерах.

---

## Ключевые преимущества

-  **Минимальное потребление памяти:** всего ~10–15 МБ RAM (против 50–150 МБ у оригинальной ноды на Node.js). * примерные данные , все зависит от вашего конфигурация Xray.
-  **Один статичный бинарник:**
-  **mTLS:** взаимная TLS-аутентификация с панелью через встроенный `SECRET_KEY`.
-  **Защита и плагины:**
  - Встроенный сбор статистики и онлайна Xray-core.
  - Поддержка Torrent Blocker (сбор репортов о BitTorrent активности).
  - Управление правилами и блокировками через `nftables`.
  - Принудительный сброс соединений и IP (`drop-ips`, `drop-users-connections`).
- **Управление и обновления:** автоматическое обновление ядра Xray (`rw-core`) и самой ноды прямо из веб-панели управления.
-  **Широкая поддержка архитектур:**
  - `x86_64` (`amd64`)
  - `arm64` (ARMv8 / AArch64)
  - `armv7` (ARMv7 / 32-bit одноплатники)
  - `mips` / `mipsle` / `mips64` / `mips64le` (роутеры и сетевые шлюзы)

---

## Архитектура и совместимость

- **Поддерживаемые панели:** [Gowave](https://github.com/aaaSaZaN/remnawave-go) и оригинальный [Remnawave?](https://github.com/remnawave).
- **Ядро Xray:** `/usr/local/bin/rw-core` (автоматически скачивается и валидируется нодой).
- **Связь с панелью:** двусторонний шифрованный mTLS-канал по порту `3000` (или кастомному).

---

## Быстрый запуск (Готовые бинарники)

Для работы ноды нужен только бинарник и строка `SECRET_KEY`, которую вы копируете из панели при добавлении ноды.

### 1. Подготовка каталога
```bash
mkdir -p /opt/remnanode && cd /opt/remnanode
```

### 2. Скачивание бинарника под вашу архитектуру

#### Для x86_64 (AMD / Intel):
```bash
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-amd64
chmod +x remnanode
```

#### Для arm64 (Apple Silicon VPS, Oracle ARM, Raspberry Pi 4/5):
```bash
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-arm64
chmod +x remnanode
```

#### Для armv7 (32-bit ARM, Orange Pi, старые Raspberry Pi):
```bash
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-armv7
chmod +x remnanode
```

#### Для MIPS / MIPSLE (роутеры OpenWrt, Keenetic и др.):
```bash
# mipsle (Little Endian):
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-mipsle
# mips (Big Endian):
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-mips
chmod +x remnanode
```

---

### 3. Настройка окружения (`.env`)

Создайте файл `/opt/remnanode/.env`:

```bash
nano /opt/remnanode/.env
```

Вставьте в него секретный ключ, скопированный из панели:

```env
SECRET_KEY=вставьте_сюда_SECRET_KEY_из_панели
NODE_PORT=3000
```

> [!TIP]
> Убедитесь, что порт `3000` (или указанный в `NODE_PORT`) открыт в фаерволе вашего сервера:
> `ufw allow 3000/tcp` или `iptables -I INPUT -p tcp --dport 3000 -j ACCEPT`.
> `Лучше вообще не открывать порт в интернет.`

---

### 4. Автозапуск через Systemd

Создайте файл службы `/etc/systemd/system/remnanode.service`:

```ini
[Unit]
Description=Remnanode (Go) Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/remnanode
ExecStart=/opt/remnanode/remnanode
Restart=always
RestartSec=5
LimitNOFILE=1000000

[Install]
WantedBy=multi-user.target
```

Активируйте и запустите службу:

```bash
systemctl daemon-reload
systemctl enable --now remnanode
systemctl status remnanode
```

---

## Переменные окружения (Environment Variables)

Все настройки задаются через файл `.env` в директории ноды либо через системные переменные:

| Переменная | По умолчанию | Описание |
| :--- | :--- | :--- |
| `SECRET_KEY` | *(обязательно)* | Ключ привязки к панели (содержит SSL-сертификаты и параметры ноды) |
| `NODE_PORT` | `3000` | Порт mTLS сервера ноды для входящих запросов от панели |
| `NFTABLES_LOGGING` | `true` | Логирование блокировок nftables в системный журнал |
| `NFTABLES_ACCEPT_REPLY_TRAFFIC` | `false` | Разрешать ответный трафик для заблокированных адресов |
| `SNI_VERIFICATION` | `false` | Дополнительная верификация SNI |
| `INTERNAL_REST_TOKEN` | `remnanode-internal-secret` | Токен для внутренних локальных вызовов |
| `XTLS_API_SOCKET_PATH` | `rw-api.sock` | Путь к Unix-сокету статистики Xray-core |

---

## Сборка из исходников

Для самостоятельной компиляции требуется **Go 1.22+**:

```bash
git clone https://github.com/aaaSaZaN/remnanode-go.git
cd remnanode-go

# Сборка под текущую ОС и архитектуру:
go build -ldflags="-s -w" -o remnanode cmd/remnanode/main.go

# Кросс-компиляция под Linux amd64:
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o remnanode-linux-amd64 cmd/remnanode/main.go

# Кросс-компиляция под Linux arm64:
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o remnanode-linux-arm64 cmd/remnanode/main.go
```

---

## Совместимость и документация

- **Панель Gowave (Go):** [https://github.com/aaaSaZaN/remnawave-go](https://github.com/aaaSaZaN/remnawave-go)
- **Официальная документация экосистемы:** [https://docs.rw](https://docs.rw)

---

## Автор и благодарности

- Разработка Go-версии (**remnanode-go**): [@aaaSaZaN](https://github.com/aaaSaZaN)
- Оригинальный проект и архитектура: [Remnawave](https://github.com/remnawave)
