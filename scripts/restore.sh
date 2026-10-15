#!/usr/bin/env bash
# ==============================================================================
# Enterprise S3-Compatible Storage Engine - Restore & Recovery Utility
# ==============================================================================
set -euo pipefail

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" || $# -lt 1 ]]; then
    echo "Usage: $0 <backup-archive.tar.gz> [--force] [--config <config.yaml>]"
    echo "Example: $0 ./data/backups/storage-backup-20261015_120000.tar.gz --force"
    exit 0
fi

ARCHIVE_PATH="$1"
shift

FORCE=false
CONFIG_FILE="config.yaml"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --force)
            FORCE=true
            shift
            ;;
        --config)
            CONFIG_FILE="$2"
            shift 2
            ;;
        *)
            echo "Unknown argument: $1"
            exit 1
            ;;
    esac
done

if [[ ! -f "${ARCHIVE_PATH}" ]]; then
    echo "Error: Archive file '${ARCHIVE_PATH}' does not exist."
    exit 1
fi

echo "=== S3 Storage Engine Restore Initiated ==="
echo "Archive: ${ARCHIVE_PATH}"
echo "Force:   ${FORCE}"

# 1. Verify archive checksum if .sha256 exists
if [[ -f "${ARCHIVE_PATH}.sha256" ]]; then
    echo "Verifying archive integrity against .sha256..."
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 -c "${ARCHIVE_PATH}.sha256"
    else
        sha256sum -c "${ARCHIVE_PATH}.sha256"
    fi
    echo "Archive SHA256 checksum verified OK."
else
    echo "Notice: No accompanying .sha256 found for archive, proceeding with internal validation."
fi

# 2. Parse target paths
TARGET_META_DB="${STORAGE_METADATA_PATH:-./data/metadata/metadata.db}"
TARGET_STORAGE="${STORAGE_DATA_ROOT:-./data/storage}"

if [[ -f "${CONFIG_FILE}" ]]; then
    if grep -q "path:" "${CONFIG_FILE}"; then
        PARSED_META=$(grep -A 2 "metadata:" "${CONFIG_FILE}" | grep "path:" | awk '{print $2}' | tr -d '"' || true)
        if [[ -n "${PARSED_META}" ]]; then TARGET_META_DB="${PARSED_META}"; fi
    fi
    if grep -q "root:" "${CONFIG_FILE}"; then
        PARSED_STORAGE=$(grep -A 2 "storage:" "${CONFIG_FILE}" | grep "root:" | awk '{print $2}' | tr -d '"' || true)
        if [[ -n "${PARSED_STORAGE}" ]]; then TARGET_STORAGE="${PARSED_STORAGE}"; fi
    fi
fi

echo "Target Metadata DB: ${TARGET_META_DB}"
echo "Target Storage Root: ${TARGET_STORAGE}"

# Check for existing data
if [[ -f "${TARGET_META_DB}" || -d "${TARGET_STORAGE}" ]]; then
    if [[ "${FORCE}" != "true" ]]; then
        echo "Error: Target metadata DB or storage directory already exists!"
        echo "To overwrite and restore, re-run with --force flag."
        exit 1
    fi
    echo "Warning: Overwrite confirmed via --force. Backing up existing state..."
    if [[ -f "${TARGET_META_DB}" ]]; then
        mv "${TARGET_META_DB}" "${TARGET_META_DB}.bak.$(date +%s)" || true
    fi
fi

WORK_DIR=$(mktemp -d "/tmp/storage-restore-work-XXXXXX")
cleanup() {
    rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

echo "Extracting backup archive to staging workspace..."
tar -xzf "${ARCHIVE_PATH}" -C "${WORK_DIR}"

BACKUP_ROOT_NAME=$(ls "${WORK_DIR}")
EXTRACT_DIR="${WORK_DIR}/${BACKUP_ROOT_NAME}"

# 3. Verify internal manifest checksums
if [[ -f "${EXTRACT_DIR}/manifest.sha256" ]]; then
    echo "Verifying staged file checksums from manifest..."
    (cd "${EXTRACT_DIR}" && {
        if command -v shasum >/dev/null 2>&1; then
            shasum -a 256 -c manifest.sha256
        else
            sha256sum -c manifest.sha256
        fi
    })
    echo "All staged objects and database files verified successfully."
fi

# 4. Restore Metadata DB
mkdir -p "$(dirname "${TARGET_META_DB}")"
if [[ -f "${EXTRACT_DIR}/metadata/metadata.db" ]]; then
    echo "Restoring metadata database to ${TARGET_META_DB}..."
    cp "${EXTRACT_DIR}/metadata/metadata.db" "${TARGET_META_DB}"
    # Remove old wal/shm if any
    rm -f "${TARGET_META_DB}-wal" "${TARGET_META_DB}-shm"
fi

# 5. Restore Storage Files
mkdir -p "${TARGET_STORAGE}"
if [[ -d "${EXTRACT_DIR}/data/storage" ]]; then
    echo "Restoring storage data files to ${TARGET_STORAGE}..."
    cp -r "${EXTRACT_DIR}/data/storage/." "${TARGET_STORAGE}/"
elif [[ -d "${EXTRACT_DIR}/storage" ]]; then
    echo "Restoring storage data files to ${TARGET_STORAGE}..."
    cp -r "${EXTRACT_DIR}/storage/." "${TARGET_STORAGE}/"
fi

echo "=== Restore Completed Successfully ==="
echo "Metadata Database: ${TARGET_META_DB}"
echo "Storage Root:      ${TARGET_STORAGE}"
echo "Storage Engine is ready to start."
