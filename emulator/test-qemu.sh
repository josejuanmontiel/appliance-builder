#!/usr/bin/env bash
set -e

# ==============================================================================
# Simulador QEMU para Kernel y Arranque de Alpine Appliance
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
BUILDER_DIR="$(dirname "$SCRIPT_DIR")"
OUTPUT_DIR="${BUILDER_DIR}/output"
WORK_DIR="${BUILDER_DIR}/work/bootfs"

if [ ! -d "$WORK_DIR" ] || [ ! -f "${WORK_DIR}/boot/vmlinuz-rpi" ]; then
    echo "Error: No se encontraron los archivos de arranque en ${WORK_DIR}/"
    echo "Ejecuta primero ./build.sh para compilar el appliance."
    exit 1
fi

echo "============================================================"
echo "  Simulador QEMU de Arranque de Kernel (Raspberry Pi)       "
echo "============================================================"

IMG_FILE=$(ls "${OUTPUT_DIR}"/*.img 2>/dev/null | head -n 1)

docker run --rm -it \
    -v "${WORK_DIR}:/bootfs:ro" \
    -v "${OUTPUT_DIR}:/output:ro" \
    alpine:3.19 \
    sh -c "
        apk add --no-cache qemu-system-arm >/dev/null 2>&1
        echo '>> Iniciando emulación QEMU con Tarjeta SD emulada...'
        echo '>> Presiona Ctrl+A luego X para salir de QEMU.'
        qemu-system-arm \
            -M raspi2b \
            -m 512M \
            -kernel /bootfs/boot/vmlinuz-rpi \
            -initrd /bootfs/boot/initramfs-rpi \
            -dtb /bootfs/bcm2836-rpi-2-b.dtb \
            -drive file=/output/$(basename "$IMG_FILE"),format=raw,if=sd \
            -append 'modules=loop,squashfs,sd-mod console=ttyAMA0,115200' \
            -nographic
    "
