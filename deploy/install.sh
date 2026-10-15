#!/usr/bin/env bash
set -euo pipefail

# S3 Storage Engine Automated Installation Script
echo "=========================================================="
echo "Installing S3-Compatible Storage Engine..."
echo "=========================================================="

INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/storage-engine"
DATA_DIR="/var/lib/storage-engine"

# 1. Provision storage system user & group
echo "Configuring dedicated system user and group..."
if ! getent group storage >/dev/null 2>&1 && ! dscl . -read /Groups/storage >/dev/null 2>&1; then
    if command -v groupadd >/dev/null 2>&1; then
        sudo groupadd -r storage
    elif command -v dscl >/dev/null 2>&1; then
        sudo dscl . -create /Groups/storage
    fi
fi

if ! id -u storage >/dev/null 2>&1; then
    if command -v useradd >/dev/null 2>&1; then
        sudo useradd -r -g storage -d "${DATA_DIR}" -s /sbin/nologin -c "S3 Storage Engine Service User" storage
    elif command -v sysadminctl >/dev/null 2>&1; then
        sudo sysadminctl -addUser storage -role daemon 2>/dev/null || true
    fi
fi

# 2. Create directories
echo "Creating system directories..."
sudo mkdir -p "${CONFIG_DIR}"
sudo mkdir -p "${DATA_DIR}/data"
sudo mkdir -p "${DATA_DIR}/metadata"

if id -u storage >/dev/null 2>&1; then
    echo "Setting directory ownership (storage:storage)..."
    sudo chown -R storage:storage "${CONFIG_DIR}" "${DATA_DIR}"
fi
sudo chmod 0750 "${CONFIG_DIR}" "${DATA_DIR}"

# 3. Build and copy binaries
echo "Compiling binaries..."
go build -o storage-server ./cmd/storage-server
go build -o storagectl ./cmd/storagectl

echo "Installing binaries to ${INSTALL_DIR}..."
sudo install -m 0755 storage-server "${INSTALL_DIR}/storage-server"
sudo install -m 0755 storagectl "${INSTALL_DIR}/storagectl"

# 4. Install default configuration if not present
if [ ! -f "${CONFIG_DIR}/config.yaml" ]; then
    echo "Writing initial configuration to ${CONFIG_DIR}/config.yaml..."
    sudo cp deploy/config.example.yaml "${CONFIG_DIR}/config.yaml"
    if id -u storage >/dev/null 2>&1; then
        sudo chown storage:storage "${CONFIG_DIR}/config.yaml"
    fi
    sudo chmod 0640 "${CONFIG_DIR}/config.yaml"
fi

# 5. Install systemd service if systemctl exists
if command -v systemctl >/dev/null 2>&1; then
    echo "Installing systemd service..."
    sudo cp deploy/systemd/storage-server.service /etc/systemd/system/
    sudo systemctl daemon-reload
    echo "To enable and start the service:"
    echo "  sudo systemctl enable --now storage-server"
fi

echo "=========================================================="
echo "Installation complete!"
echo "Server binary: ${INSTALL_DIR}/storage-server"
echo "CLI binary:    ${INSTALL_DIR}/storagectl"
echo "Config file:   ${CONFIG_DIR}/config.yaml"
echo "Data folder:   ${DATA_DIR}"
echo "=========================================================="
