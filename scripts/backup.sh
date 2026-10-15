#!/usr/bin/env bash
# ==============================================================================
# Enterprise S3-Compatible Storage Engine - Online Backup Utility
# ==============================================================================
set -euo pipefail

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    echo "Usage: $0 [config.yaml] [backup_destination_dir]"
    echo "Example: $0 config.yaml ./data/backups"
    exit 0
fi

CONFIG_FILE="${1:-config.yaml}"
BACKUP_DEST_DIR="${2:-${BACKUP_DEST_DIR:-./data/backups}}"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BACKUP_NAME="storage-backup-${TIMESTAMP}"
WORK_DIR=$(mktemp -d "/tmp/storage-backup-work-XXXXXX")

cleanup() {
    rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

echo "=== S3 Storage Engine Backup Initiated ==="
echo "Timestamp:   ${TIMESTAMP}"
echo "Config File: ${CONFIG_FILE}"
echo "Backup Dest: ${BACKUP_DEST_DIR}"

mkdir -p "${BACKUP_DEST_DIR}"
mkdir -p "${WORK_DIR}/${BACKUP_NAME}/metadata"
mkdir -p "${WORK_DIR}/${BACKUP_NAME}/storage"

# 1. Read metadata and storage paths from config or environment
METADATA_DB="${STORAGE_METADATA_PATH:-./data/metadata/metadata.db}"
STORAGE_ROOT="${STORAGE_DATA_ROOT:-./data/storage}"
ADMIN_TOKEN="${STORAGE_ADMIN_TOKEN:-}"

if [[ -f "${CONFIG_FILE}" ]]; then
    if grep -q "path:" "${CONFIG_FILE}"; then
        PARSED_META=$(grep -A 2 "metadata:" "${CONFIG_FILE}" | grep "path:" | awk '{print $2}' | tr -d '"' || true)
        if [[ -n "${PARSED_META}" ]]; then METADATA_DB="${PARSED_META}"; fi
    fi
    if grep -q "root:" "${CONFIG_FILE}"; then
        PARSED_STORAGE=$(grep -A 2 "storage:" "${CONFIG_FILE}" | grep "root:" | awk '{print $2}' | tr -d '"' || true)
        if [[ -n "${PARSED_STORAGE}" ]]; then STORAGE_ROOT="${PARSED_STORAGE}"; fi
    fi
    if grep -q "admin_token:" "${CONFIG_FILE}"; then
        PARSED_TOKEN=$(grep -A 5 "security:" "${CONFIG_FILE}" | grep "admin_token:" | awk '{print $2}' | tr -d '"' || true)
        if [[ -n "${PARSED_TOKEN}" ]]; then ADMIN_TOKEN="${PARSED_TOKEN}"; fi
    fi
fi

echo "Metadata DB:  ${METADATA_DB}"
echo "Storage Root: ${STORAGE_ROOT}"

# 2. Perform online SQLite consistent backup
SNAP_DB="${WORK_DIR}/${BACKUP_NAME}/metadata/metadata.db"
BACKUP_SUCCESS=false

# Try online API backup first if server is running
if [[ -n "${ADMIN_TOKEN}" ]]; then
    echo "Attempting online API metadata backup via admin endpoint..."
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://127.0.0.1:9001/api/v1/system/backup" \
        -H "X-Admin-Token: ${ADMIN_TOKEN}" \
        -H "Content-Type: application/json" \
        -d "{\"destination\":\"${SNAP_DB}\"}" 2>/dev/null || true)
    if [[ "${HTTP_CODE}" == "200" && -f "${SNAP_DB}" ]]; then
        echo "Online live metadata backup succeeded via Admin API."
        BACKUP_SUCCESS=true
    fi
fi

if [[ "${BACKUP_SUCCESS}" != "true" ]]; then
    echo "Using direct SQLite online vacuum backup..."
    if command -v sqlite3 >/dev/null 2>&1 && [[ -f "${METADATA_DB}" ]]; then
        sqlite3 "${METADATA_DB}" "VACUUM INTO '${SNAP_DB}';"
        BACKUP_SUCCESS=true
    elif [[ -f "${METADATA_DB}" ]]; then
        # Fallback copy if sqlite3 CLI not found
        cp "${METADATA_DB}" "${SNAP_DB}"
        if [[ -f "${METADATA_DB}-wal" ]]; then cp "${METADATA_DB}-wal" "${WORK_DIR}/${BACKUP_NAME}/metadata/"; fi
        if [[ -f "${METADATA_DB}-shm" ]]; then cp "${METADATA_DB}-shm" "${WORK_DIR}/${BACKUP_NAME}/metadata/"; fi
        BACKUP_SUCCESS=true
    else
        echo "Warning: Metadata DB not found at ${METADATA_DB}. Creating empty metadata marker."
        touch "${SNAP_DB}"
    fi
fi

# 3. Snapshot storage data and generate checksum manifest
echo "Snapshotting storage data files..."
if [[ -d "${STORAGE_ROOT}" ]]; then
    # Copy storage tree excluding temp and lock files
    if command -v rsync >/dev/null 2>&1; then
        rsync -a --exclude="*.tmp.*" "${STORAGE_ROOT}/" "${WORK_DIR}/${BACKUP_NAME}/storage/" 2>/dev/null || true
    else
        (cd "${STORAGE_ROOT}" && tar --exclude="*.tmp.*" -cf - .) | (cd "${WORK_DIR}/${BACKUP_NAME}/storage" && tar -xf -)
    fi
fi

echo "Generating file checksum manifest..."
MANIFEST_FILE="${WORK_DIR}/${BACKUP_NAME}/manifest.sha256"
(cd "${WORK_DIR}/${BACKUP_NAME}" && find . -type f ! -name "manifest.sha256" -exec shasum -a 256 {} + > "manifest.sha256" 2>/dev/null || \
 find . -type f ! -name "manifest.sha256" -exec sha256sum {} + > "manifest.sha256")

# 4. Package archive
ARCHIVE_PATH="${BACKUP_DEST_DIR}/${BACKUP_NAME}.tar.gz"
echo "Creating compressed backup tarball: ${ARCHIVE_PATH}..."
tar -czf "${ARCHIVE_PATH}" -C "${WORK_DIR}" "${BACKUP_NAME}"

# 5. Checksum of archive itself
if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${ARCHIVE_PATH}" > "${ARCHIVE_PATH}.sha256"
else
    sha256sum "${ARCHIVE_PATH}" > "${ARCHIVE_PATH}.sha256"
fi

ARCHIVE_SIZE=$(ls -lh "${ARCHIVE_PATH}" | awk '{print $5}')
echo "=== Backup Completed Successfully ==="
echo "Archive:      ${ARCHIVE_PATH}"
echo "Archive Size: ${ARCHIVE_SIZE}"
echo "Checksum:     ${ARCHIVE_PATH}.sha256"
