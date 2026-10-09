#!/usr/bin/env bash
# ==============================================================================
# Remnanode (Go) Migration & Auto-Installer Script
# Migrates from Docker (remnawave/node) to Native Remnanode (Go) with ~0s Downtime
# Repository: https://github.com/aaaSaZaN/remnanode-go
# ==============================================================================

set -e

COLOR_RESET="\033[0m"
COLOR_RED="\033[1;31m"
COLOR_GREEN="\033[1;32m"
COLOR_YELLOW="\033[1;33m"
COLOR_CYAN="\033[1;36m"
COLOR_BOLD="\033[1m"

log_info() { echo -e "${COLOR_CYAN}[INFO]${COLOR_RESET} $1"; }
log_success() { echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $1"; }
log_warn() { echo -e "${COLOR_YELLOW}[WARN]${COLOR_RESET} $1"; }
log_error() { echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $1"; }

echo -e "${COLOR_CYAN}${COLOR_BOLD}"
echo "=================================================================="
echo "    Remnanode Migration Tool (Docker TS  ➔  Native Go)"
echo "    Near-Zero Downtime Switcher"
echo "=================================================================="
echo -e "${COLOR_RESET}"

if [ "$(id -u)" -ne 0 ]; then
    log_error "Этот скрипт должен выполняться от имени root (или через sudo)."
    exit 1
fi

if ! command -v systemctl >/dev/null 2>&1; then
    log_error "Скрипт поддерживает системы с systemd (Debian, Ubuntu, CentOS, Fedora, Alma, Rocky)."
    exit 1
fi

ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)     BIN_ARCH="amd64" ;;
    aarch64|arm64)    BIN_ARCH="arm64" ;;
    armv7*|armhf)     BIN_ARCH="armv7" ;;
    mipsle)           BIN_ARCH="mipsle" ;;
    mips)             BIN_ARCH="mips" ;;
    mips64le)         BIN_ARCH="mips64le" ;;
    mips64)           BIN_ARCH="mips64" ;;
    *)
        log_error "Неподдерживаемая архитектура процессора: $ARCH"
        exit 1
        ;;
esac
log_info "Определена архитектура: ${COLOR_BOLD}${BIN_ARCH}${COLOR_RESET}"

DOCKER_FOUND=false
CONTAINER_NAME=""
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    CONTAINER_NAME=$(docker ps -a --format "{{.Names}}\t{{.Image}}" | grep -E "(remnawave/node|remnanode)" | awk "{print \$1}" | head -n 1 || true)
    if [ -n "$CONTAINER_NAME" ]; then
        DOCKER_FOUND=true
    fi
fi

SECRET_KEY=""
NODE_PORT=""

if [ "$DOCKER_FOUND" = true ]; then
    log_info "Обнаружен Docker контейнер Remnanode: ${COLOR_BOLD}${CONTAINER_NAME}${COLOR_RESET}"
    ENV_RAW=$(docker inspect "$CONTAINER_NAME" --format "{{range .Config.Env}}{{println .}}{{end}}" 2>/dev/null || true)
    SECRET_KEY=$(echo "$ENV_RAW" | grep -m1 "^SECRET_KEY=" | sed "s/^SECRET_KEY=//" | sed "s/^\"\(.*\)\"$/\1/" | sed "s/^'\(.*\)'$/\1/" || true)
    NODE_PORT=$(echo "$ENV_RAW" | grep -m1 "^NODE_PORT=" | sed "s/^NODE_PORT=//" | sed "s/^\"\(.*\)\"$/\1/" | sed "s/^'\(.*\)'$/\1/" || true)
fi

if [ -z "$SECRET_KEY" ]; then
    for env_candidate in "/opt/remnanode/.env" "/root/remnanode/.env" "./.env" "./docker-compose.yml" "/opt/remnanode/docker-compose.yml"; do
        if [ -f "$env_candidate" ]; then
            log_info "Поиск конфигурации в $env_candidate..."
            if [ -z "$SECRET_KEY" ]; then
                SECRET_KEY=$(grep -m1 -E "(SECRET_KEY=|- SECRET_KEY=)" "$env_candidate" | sed -e "s/.*SECRET_KEY=//" -e "s/.*SECRET_KEY: //" | tr -d "\"" | tr -d "'" | tr -d " " || true)
            fi
            if [ -z "$NODE_PORT" ]; then
                NODE_PORT=$(grep -m1 -E "(NODE_PORT=|- NODE_PORT=)" "$env_candidate" | sed -e "s/.*NODE_PORT=//" -e "s/.*NODE_PORT: //" | tr -d "\"" | tr -d "'" | tr -d " " || true)
            fi
        fi
    done
fi

if [ -z "$SECRET_KEY" ]; then
    log_warn "SECRET_KEY не найден автоматически."
    if [ -t 0 ]; then
        read -r -p "Введите SECRET_KEY ноды (из панели Gowave/Remnawave): " SECRET_KEY
    elif [ -e /dev/tty ]; then
        read -r -p "Введите SECRET_KEY ноды (из панели Gowave/Remnawave): " SECRET_KEY </dev/tty
    fi
    if [ -z "$SECRET_KEY" ]; then
        log_error "SECRET_KEY обязателен для работы ноды. Миграция прервана."
        exit 1
    fi
fi

if [ -z "$NODE_PORT" ]; then
    NODE_PORT="3000"
fi

log_info "Конфигурация: порт ${COLOR_BOLD}${NODE_PORT}${COLOR_RESET}, SECRET_KEY получен."

INSTALL_DIR="/opt/remnanode"
mkdir -p "$INSTALL_DIR"
cd "$INSTALL_DIR"

if [ -f "$INSTALL_DIR/docker-compose.yml" ]; then
    cp "$INSTALL_DIR/docker-compose.yml" "$INSTALL_DIR/docker-compose.yml.bak" 2>/dev/null || true
fi
if [ -f "$INSTALL_DIR/.env" ]; then
    cp "$INSTALL_DIR/.env" "$INSTALL_DIR/.env.bak" 2>/dev/null || true
fi

cat <<EOF > "$INSTALL_DIR/.env"
NODE_PORT=$NODE_PORT
SECRET_KEY="$SECRET_KEY"
EOF
chmod 600 "$INSTALL_DIR/.env"
log_info "Файл конфигурации сохранен в $INSTALL_DIR/.env"

DOWNLOAD_URL="${REMNANODE_DOWNLOAD_URL:-https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-${BIN_ARCH}}"

if [ -f "./remnanode" ] && [ -x "./remnanode" ]; then
    log_info "Используем готовый локальный бинарник ./remnanode..."
    cp -f "./remnanode" "$INSTALL_DIR/remnanode"
elif [ -f "/tmp/remnanode" ] && [ -x "/tmp/remnanode" ]; then
    log_info "Используем готовый бинарник из /tmp/remnanode..."
    cp -f "/tmp/remnanode" "$INSTALL_DIR/remnanode"
else
    log_info "Скачивание бинарника remnanode-linux-${BIN_ARCH}..."
    TMP_BIN="$INSTALL_DIR/remnanode.tmp"
    if command -v curl >/dev/null 2>&1; then
        curl -fSL -o "$TMP_BIN" "$DOWNLOAD_URL"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$TMP_BIN" "$DOWNLOAD_URL"
    else
        log_error "Не найден ни curl, ни wget."
        exit 1
    fi
    chmod +x "$TMP_BIN"
    mv -f "$TMP_BIN" "$INSTALL_DIR/remnanode"
fi
chmod +x "$INSTALL_DIR/remnanode"
log_success "Бинарник установлен в $INSTALL_DIR/remnanode"

log_info "Предварительная загрузка ядра Xray-core..."
if [ ! -f "$INSTALL_DIR/rw-core" ] && [ ! -f "/usr/local/bin/rw-core" ]; then
    "$INSTALL_DIR/remnanode" core install -y || log_warn "Предустановка ядра завершилась с кодом, нода скачает его при старте."
fi

log_info "Настройка службы systemd /etc/systemd/system/remnanode.service..."
cat <<EOF > /etc/systemd/system/remnanode.service
[Unit]
Description=Remnanode (Go) Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/remnanode
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

log_info "Подготовка геофайлов (geoip.dat, geosite.dat)..."
mkdir -p "$INSTALL_DIR" /usr/local/share/xray

if [ "$DOCKER_FOUND" = true ]; then
    docker cp "$CONTAINER_NAME:/usr/local/share/xray/geoip.dat" "$INSTALL_DIR/geoip.dat" 2>/dev/null || true
    docker cp "$CONTAINER_NAME:/usr/local/share/xray/geosite.dat" "$INSTALL_DIR/geosite.dat" 2>/dev/null || true
fi

if [ ! -f "$INSTALL_DIR/geoip.dat" ] || [ ! -s "$INSTALL_DIR/geoip.dat" ]; then
    log_info "Скачивание актуального geoip.dat..."
    curl -fsSL -o "$INSTALL_DIR/geoip.dat" https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat || true
fi
if [ ! -f "$INSTALL_DIR/geosite.dat" ] || [ ! -s "$INSTALL_DIR/geosite.dat" ]; then
    log_info "Скачивание актуального geosite.dat..."
    curl -fsSL -o "$INSTALL_DIR/geosite.dat" https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat || true
fi

[ -f "$INSTALL_DIR/geoip.dat" ] && cp -f "$INSTALL_DIR/geoip.dat" /usr/local/share/xray/geoip.dat 2>/dev/null || true
[ -f "$INSTALL_DIR/geosite.dat" ] && cp -f "$INSTALL_DIR/geosite.dat" /usr/local/share/xray/geosite.dat 2>/dev/null || true

systemctl daemon-reload

WAS_RUNNING=false
if [ "$DOCKER_FOUND" = true ]; then
    if [ "$(docker inspect -f "{{.State.Running}}" "$CONTAINER_NAME" 2>/dev/null)" = "true" ]; then
        WAS_RUNNING=true
        log_info "Мгновенное переключение: остановка Docker ($CONTAINER_NAME) и запуск Go службы..."
        docker update --restart=no "$CONTAINER_NAME" >/dev/null 2>&1 || true
        docker stop -t 2 "$CONTAINER_NAME" >/dev/null 2>&1 || true
    fi
fi

log_info "Активация и старт службы remnanode..."
systemctl enable --now remnanode

sleep 2
if systemctl is-active --quiet remnanode; then
    log_success "Служба remnanode (Go) успешно запущена!"

    ln -sf "$INSTALL_DIR/remnanode" /usr/local/bin/remnanode 2>/dev/null || true
    ln -sf "$INSTALL_DIR/remnanode" /usr/local/bin/remnanode-go 2>/dev/null || true
    ln -sf "$INSTALL_DIR/remnanode" /usr/local/bin/xlogs 2>/dev/null || true

    echo ""
    echo -e "${COLOR_GREEN}${COLOR_BOLD}==================================================================${COLOR_RESET}"
    echo -e "${COLOR_GREEN}${COLOR_BOLD}   МИГРАЦИЯ УСПЕШНО ЗАВЕРШЕНА! ДАУНТАЙМ СОСТАВИЛ ~1 СЕКУНДУ.       ${COLOR_RESET}"
    echo -e "${COLOR_GREEN}${COLOR_BOLD}==================================================================${COLOR_RESET}"
    echo ""
    "$INSTALL_DIR/remnanode" status || true
    echo ""
    echo -e "${COLOR_CYAN}Полезные команды из любой папки:${COLOR_RESET}"
    echo -e "  ${COLOR_BOLD}remnanode status${COLOR_RESET}   - статус ноды и ядра"
    echo -e "  ${COLOR_BOLD}remnanode restart${COLOR_RESET}  - быстрый перезапуск службы"
    echo -e "  ${COLOR_BOLD}xlogs -f${COLOR_RESET}           - цветной мониторинг логов Xray"
    echo -e "  ${COLOR_BOLD}remnanode logs -f${COLOR_RESET}  - системные логи сервиса ноды"
    echo ""
    if [ "$WAS_RUNNING" = true ]; then
        echo -e "${COLOR_YELLOW}Старый контейнер "$CONTAINER_NAME" остановлен и снят с автозапуска.${COLOR_RESET}"
        echo -e "После проверки работы в панели вы можете окончательно удалить контейнер:"
        echo -e "  ${COLOR_BOLD}docker rm $CONTAINER_NAME${COLOR_RESET}"
    fi
    echo ""
else
    log_error "Служба remnanode не смогла запуститься!"
    if [ "$WAS_RUNNING" = true ]; then
        log_warn "Выполняем АВТОМАТИЧЕСКИЙ ОТКАТ к Docker контейнеру..."
        systemctl stop remnanode 2>/dev/null || true
        docker start "$CONTAINER_NAME" >/dev/null 2>&1 || true
        log_info "Docker контейнер $CONTAINER_NAME восстановлен и работает."
    fi
    echo ""
    log_error "Логи ошибки службы remnanode:"
    journalctl -u remnanode --no-pager -n 25 || true
    exit 1
fi
