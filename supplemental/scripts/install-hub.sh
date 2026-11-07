#!/bin/sh

is_freebsd() {
  [ "$(uname -s)" = "FreeBSD" ]
}

# Function to ensure the proxy URL ends with a /
ensure_trailing_slash() {
  if [ -n "$1" ]; then
    case "$1" in
    */) echo "$1" ;;
    *) echo "$1/" ;;
    esac
  else
    echo "$1"
  fi
}

# Generate FreeBSD rc service content
generate_freebsd_rc_service() {
  cat <<'EOF'
#!/bin/sh

# PROVIDE: beszel_hub
# REQUIRE: DAEMON NETWORKING
# BEFORE: LOGIN
# KEYWORD: shutdown

# Add the following lines to /etc/rc.conf to configure vpsmonitor Hub:
#
# beszel_hub_enable (bool):   Set to YES to enable vpsmonitor Hub
#                             Default: YES
# beszel_hub_port (str):      Port to listen on
#                             Default: 8090
# beszel_hub_user (str):      vpsmonitor Hub daemon user
#                             Default: vpsmonitor
# beszel_hub_bin (str):       Path to the vpsmonitor binary
#                             Default: /usr/local/sbin/vpsmonitor
# beszel_hub_data (str):      Path to the vpsmonitor data directory
#                             Default: /usr/local/etc/vpsmonitor/beszel_data
# beszel_hub_flags (str):     Extra flags passed to vpsmonitor command invocation
#                             Default:

. /etc/rc.subr

name="beszel_hub"
rcvar=beszel_hub_enable

load_rc_config $name
: ${beszel_hub_enable:="YES"}
: ${beszel_hub_port:="8090"}
: ${beszel_hub_user:="vpsmonitor"}
: ${beszel_hub_flags:=""}
: ${beszel_hub_bin:="/usr/local/sbin/vpsmonitor"}
: ${beszel_hub_data:="/usr/local/etc/vpsmonitor/beszel_data"}

logfile="/var/log/${name}.log"
pidfile="/var/run/${name}.pid"

procname="/usr/sbin/daemon"
start_precmd="${name}_prestart"
start_cmd="${name}_start"
stop_cmd="${name}_stop"

extra_commands="upgrade"
upgrade_cmd="beszel_hub_upgrade"

beszel_hub_prestart()
{
    if [ ! -d "${beszel_hub_data}" ]; then
        echo "Creating data directory ${beszel_hub_data}"
        mkdir -p "${beszel_hub_data}"
        chown "${beszel_hub_user}:${beszel_hub_user}" "${beszel_hub_data}"
    fi
}

beszel_hub_start()
{
    echo "Starting ${name}"
    cd "$(dirname "${beszel_hub_data}")" || exit 1
    /usr/sbin/daemon -f \
            -P "${pidfile}" \
            -o "${logfile}" \
            -u "${beszel_hub_user}" \
            "${beszel_hub_bin}" serve --http "0.0.0.0:${beszel_hub_port}" ${beszel_hub_flags}
}

beszel_hub_stop()
{
    pid="$(check_pidfile "${pidfile}" "${procname}")"
    if [ -n "${pid}" ]; then
        echo "Stopping ${name} (pid=${pid})"
        kill -- "-${pid}"
        wait_for_pids "${pid}"
    else
        echo "${name} isn't running"
    fi
}

beszel_hub_upgrade()
{
    echo "Upgrading ${name}"
    if command -v sudo >/dev/null; then
        sudo -u "${beszel_hub_user}" -- "${beszel_hub_bin}" update
    else
        su -m "${beszel_hub_user}" -c "${beszel_hub_bin} update"
    fi
}

run_rc_command "$1"
EOF
}

# Detect system architecture
detect_architecture() {
  arch=$(uname -m)
  case "$arch" in
    x86_64)
      arch="amd64"
      ;;
    armv7l)
      arch="arm"
      ;;
    aarch64)
      arch="arm64"
      ;;
  esac
  echo "$arch"
}

# Build sudo args by properly quoting everything
build_sudo_args() {
  QUOTED_ARGS=""
  while [ $# -gt 0 ]; do
    if [ -n "$QUOTED_ARGS" ]; then
      QUOTED_ARGS="$QUOTED_ARGS "
    fi
    QUOTED_ARGS="$QUOTED_ARGS'$(echo "$1" | sed "s/'/'\\\\''/g")'"
    shift
  done
  echo "$QUOTED_ARGS"
}

# Check if running as root and re-execute with sudo if needed
if [ "$(id -u)" != "0" ]; then
  if command -v sudo >/dev/null 2>&1; then
    SUDO_ARGS=$(build_sudo_args "$@")
    eval "exec sudo $0 $SUDO_ARGS"
  else
    echo "This script must be run as root. Please either:"
    echo "1. Run this script as root (su root)"
    echo "2. Install sudo and run with sudo"
    exit 1
  fi
fi

# Define default values
PORT=8090
GITHUB_URL="https://github.com"
AUTO_UPDATE_FLAG="false"
UNINSTALL=false

# Parse command line arguments
while [ $# -gt 0 ]; do
  case "$1" in
    -u)
      UNINSTALL=true
      shift
      ;;
    -h|--help)
      printf "vpsmonitor Hub installation script\n\n"
      printf "Usage: ./install-hub.sh [options]\n\n"
      printf "Options: \n"
      printf "  -u           : Uninstall the vpsmonitor Hub\n"
      printf "  -p <port>    : Specify a port number (default: 8090)\n"
      printf "  -c, --mirror [URL] : Use a GitHub mirror/proxy URL (default: https://gh.vpsmonitor.dev)\n"
      printf "  --auto-update : Enable automatic daily updates (disabled by default)\n"
      printf "  -h, --help   : Display this help message\n"
      exit 0
      ;;
    -p)
      shift
      PORT="$1"
      shift
      ;;
    -c | --mirror)
      shift
      if [ -n "$1" ] && ! echo "$1" | grep -q '^-'; then
        GITHUB_URL="$(ensure_trailing_slash "$1")https://github.com"
        shift
      else
        GITHUB_URL="https://gh.vpsmonitor.dev"
      fi
      ;;
    --auto-update)
      AUTO_UPDATE_FLAG="true"
      shift
      ;;
    *)
      echo "Invalid option: $1" >&2
      exit 1
      ;;
  esac
done

# Set paths based on operating system
if is_freebsd; then
  HUB_DIR="/usr/local/etc/vpsmonitor"
  BIN_PATH="/usr/local/sbin/vpsmonitor"
else
  HUB_DIR="/opt/vpsmonitor"
  BIN_PATH="/opt/vpsmonitor/vpsmonitor"
fi

# Uninstall process
if [ "$UNINSTALL" = true ]; then
  if is_freebsd; then
    echo "Stopping and disabling the vpsmonitor Hub service..."
    service vpsmonitor-hub stop 2>/dev/null
    sysrc beszel_hub_enable="NO" 2>/dev/null

    echo "Removing the FreeBSD service files..."
    rm -f /usr/local/etc/rc.d/vpsmonitor-hub

    echo "Removing the daily update cron job..."
    rm -f /etc/cron.d/vpsmonitor-hub

    echo "Removing log files..."
    rm -f /var/log/beszel_hub.log

    echo "Removing the vpsmonitor Hub binary and data..."
    rm -f "$BIN_PATH"
    rm -rf "$HUB_DIR"

    echo "Removing the dedicated user..."
    pw user del vpsmonitor 2>/dev/null

    echo "The vpsmonitor Hub has been uninstalled successfully!"
    exit 0
  else
    # Stop and disable the vpsmonitor Hub service
    echo "Stopping and disabling the vpsmonitor Hub service..."
    systemctl stop vpsmonitor-hub.service
    systemctl disable vpsmonitor-hub.service

    # Remove the systemd service file
    echo "Removing the systemd service file..."
    rm -f /etc/systemd/system/vpsmonitor-hub.service

    # Remove the update timer and service if they exist
    echo "Removing the daily update service and timer..."
    systemctl stop vpsmonitor-hub-update.timer 2>/dev/null
    systemctl disable vpsmonitor-hub-update.timer 2>/dev/null
    rm -f /etc/systemd/system/vpsmonitor-hub-update.service
    rm -f /etc/systemd/system/vpsmonitor-hub-update.timer

    # Reload the systemd daemon
    echo "Reloading the systemd daemon..."
    systemctl daemon-reload

    # Remove the vpsmonitor Hub binary and data
    echo "Removing the vpsmonitor Hub binary and data..."
    rm -rf "$HUB_DIR"

    # Remove the dedicated user
    echo "Removing the dedicated user..."
    userdel vpsmonitor 2>/dev/null

    echo "The vpsmonitor Hub has been uninstalled successfully!"
    exit 0
  fi
fi

# Function to check if a package is installed
package_installed() {
  command -v "$1" >/dev/null 2>&1
}

# Check for package manager and install necessary packages if not installed
if package_installed pkg && is_freebsd; then
  if ! package_installed tar || ! package_installed curl; then
    pkg update
    pkg install -y gtar curl
  fi
elif package_installed apt-get; then
  if ! package_installed tar || ! package_installed curl; then
    apt-get update
    apt-get install -y tar curl
  fi
elif package_installed yum; then
  if ! package_installed tar || ! package_installed curl; then
    yum install -y tar curl
  fi
elif package_installed pacman; then
  if ! package_installed tar || ! package_installed curl; then
    pacman -Sy --noconfirm tar curl
  fi
else
  echo "Warning: Please ensure 'tar' and 'curl' are installed."
fi

# Create a dedicated user for the service if it doesn't exist
echo "Creating a dedicated user for the vpsmonitor Hub service..."
if is_freebsd; then
  if ! id -u vpsmonitor >/dev/null 2>&1; then
    pw user add vpsmonitor -d /nonexistent -s /usr/sbin/nologin -c "vpsmonitor user"
  fi
else
  if ! id -u vpsmonitor >/dev/null 2>&1; then
    useradd -M -s /bin/false vpsmonitor
  fi
fi

# Create the directory for the vpsmonitor Hub
echo "Creating the directory for the vpsmonitor Hub..."
mkdir -p "$HUB_DIR/beszel_data"
chown -R vpsmonitor:vpsmonitor "$HUB_DIR"
chmod 755 "$HUB_DIR"

# Download and install the vpsmonitor Hub
echo "Downloading and installing the vpsmonitor Hub..."

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(detect_architecture)
FILE_NAME="beszel_${OS}_${ARCH}.tar.gz"

TEMP_DIR=$(mktemp -d)
ARCHIVE_PATH="$TEMP_DIR/$FILE_NAME"
DOWNLOAD_URL="$GITHUB_URL/henrygd/vpsmonitor/releases/latest/download/$FILE_NAME"

if ! curl -fL# --retry 3 --retry-delay 2 --connect-timeout 10 "$DOWNLOAD_URL" -o "$ARCHIVE_PATH"; then
  echo "Failed to download the vpsmonitor Hub from:"
  echo "$DOWNLOAD_URL"
  echo "Try again with --mirror (or --mirror <url>) if GitHub is not reachable."
  rm -rf "$TEMP_DIR"
  exit 1
fi

if ! tar -tzf "$ARCHIVE_PATH" >/dev/null 2>&1; then
  echo "Downloaded archive is invalid or incomplete (possible network/proxy issue)."
  echo "Try again with --mirror (or --mirror <url>) if the download path is unstable."
  rm -rf "$TEMP_DIR"
  exit 1
fi

if ! tar -xzf "$ARCHIVE_PATH" -C "$TEMP_DIR" vpsmonitor; then
  echo "Failed to extract vpsmonitor from archive."
  rm -rf "$TEMP_DIR"
  exit 1
fi

if [ ! -s "$TEMP_DIR/vpsmonitor" ]; then
  echo "Downloaded binary is missing or empty."
  rm -rf "$TEMP_DIR"
  exit 1
fi

chmod +x "$TEMP_DIR/vpsmonitor"
mv "$TEMP_DIR/vpsmonitor" "$BIN_PATH"
chown vpsmonitor:vpsmonitor "$BIN_PATH"
rm -rf "$TEMP_DIR"

if is_freebsd; then
  echo "Creating FreeBSD rc service..."

  # Create the rc service file
  generate_freebsd_rc_service > /usr/local/etc/rc.d/vpsmonitor-hub

  # Set proper permissions for the rc script
  chmod 755 /usr/local/etc/rc.d/vpsmonitor-hub

  # Configure the port
  sysrc beszel_hub_port="$PORT"

  # Enable and start the service
  echo "Enabling and starting the vpsmonitor Hub service..."
  sysrc beszel_hub_enable="YES"
  service vpsmonitor-hub restart

  # Check if service started successfully
  sleep 2
  if ! service vpsmonitor-hub status | grep -q "is running"; then
    echo "Error: The vpsmonitor Hub service failed to start. Checking logs..."
    tail -n 20 /var/log/beszel_hub.log
    exit 1
  fi

  # Auto-update service for FreeBSD
  if [ "$AUTO_UPDATE_FLAG" = "true" ]; then
    echo "Setting up daily automatic updates for vpsmonitor-hub..."

    # Create cron job in /etc/cron.d
    cat >/etc/cron.d/vpsmonitor-hub <<EOF
# vpsmonitor Hub daily update job
12 8 * * * root $BIN_PATH update >/dev/null 2>&1
EOF
    chmod 644 /etc/cron.d/vpsmonitor-hub
    printf "\nDaily updates have been enabled via /etc/cron.d.\n"
  fi

  # Check service status
  if ! service vpsmonitor-hub status >/dev/null 2>&1; then
    echo "Error: The vpsmonitor Hub service is not running."
    service vpsmonitor-hub status
    exit 1
  fi

else
  # Original systemd service installation code
  printf "Creating the systemd service for the vpsmonitor Hub...\n"
  cat >/etc/systemd/system/vpsmonitor-hub.service <<EOF
[Unit]
Description=vpsmonitor Hub Service
After=network.target

[Service]
ExecStart=$BIN_PATH serve --http "0.0.0.0:$PORT"
WorkingDirectory=$HUB_DIR
User=vpsmonitor
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

  # Load and start the service
  printf "Loading and starting the vpsmonitor Hub service...\n"
  systemctl daemon-reload
  systemctl enable --quiet vpsmonitor-hub.service
  systemctl start --quiet vpsmonitor-hub.service

  # Wait for the service to start or fail
  sleep 2

  # Check if the service is running
  if [ "$(systemctl is-active vpsmonitor-hub.service)" != "active" ]; then
    echo "Error: The vpsmonitor Hub service is not running."
    echo "$(systemctl status vpsmonitor-hub.service)"
    exit 1
  fi

  # Enable auto-update if flag is set to true
  if [ "$AUTO_UPDATE_FLAG" = "true" ]; then
    echo "Setting up daily automatic updates for vpsmonitor-hub..."

    # Create systemd service for the daily update
    cat >/etc/systemd/system/vpsmonitor-hub-update.service <<EOF
[Unit]
Description=Update vpsmonitor-hub if needed
Wants=vpsmonitor-hub.service

[Service]
Type=oneshot
ExecStart=$BIN_PATH update
EOF

    # Create systemd timer for the daily update
    cat >/etc/systemd/system/vpsmonitor-hub-update.timer <<EOF
[Unit]
Description=Run vpsmonitor-hub update daily

[Timer]
OnCalendar=daily
Persistent=true
RandomizedDelaySec=4h

[Install]
WantedBy=timers.target
EOF

    systemctl daemon-reload
    systemctl enable --now vpsmonitor-hub-update.timer

    printf "\nDaily updates have been enabled.\n"
  fi
fi

printf "\n\033[32mvpsmonitor Hub has been installed successfully! It is now accessible on port $PORT.\033[0m\n"
