#!/usr/bin/env bash
set -e

# ==============================================================================
# Paso 4: Generar Entregables Finales (.tar.gz para SD y .img.gz particionada)
# ==============================================================================

WORKDIR="${1:-/workspace/work}"
OUTPUT_DIR="${2:-/workspace/output}"
CONFIG_ENV="${3:-/workspace/config.env}"

[ -f "$CONFIG_ENV" ] && source "$CONFIG_ENV"

BOOTFS="${WORKDIR}/bootfs"
mkdir -p "${OUTPUT_DIR}"

echo ">> [4/4] Ensamblando entregables de arranque (Tarball + Imagen particionada)..."

# 1. cmdline.txt: sin quiet para ver output durante instalación
cat <<EOF > "${BOOTFS}/cmdline.txt"
modules=loop,squashfs,sd-mod,usb-storage console=tty1 debug_init
EOF

# 2. config.txt: idéntico al oficial de Alpine + followkernel para asegurar placement correcto
# en memoria (sin followkernel algunos GPU firmware antiguos ignoran el initramfs)
cat <<EOF > "${BOOTFS}/config.txt"
# do not modify this file as it will be overwritten on upgrade.
# create and/or modify usercfg.txt instead.
# https://www.raspberrypi.com/documentation/computers/config_txt.html

kernel=boot/vmlinuz-rpi
initramfs boot/initramfs-rpi followkernel
arm_64bit=0
include usercfg.txt
EOF

# usercfg.txt: opciones de hardware específicas del appliance
cat <<EOF > "${BOOTFS}/usercfg.txt"
# Hardware config for appliance
hdmi_force_hotplug=1
hdmi_drive=2
disable_splash=0
enable_uart=1
gpu_mem=${GPU_MEM:-64}
dtoverlay=miniuart-bt
EOF

# 3. Eliminar enlaces simbólicos y asegurar permisos de lectura universales
chmod -R a+rX "${BOOTFS}"
find "${BOOTFS}" -type l -delete 2>/dev/null || true
rm -f "${BOOTFS}/boot/boot" 2>/dev/null || true

NAME_PREFIX="${APPLIANCE_NAME:-appliance}-${TARGET_BOARD:-rpi}"
TARBALL_FILE="${OUTPUT_DIR}/${NAME_PREFIX}.tar.gz"
IMG_FILE="${OUTPUT_DIR}/${NAME_PREFIX}.img"
IMG_GZ_FILE="${IMG_FILE}.gz"

# 4. Generar Tarball para instalación directa en FAT32
echo "  Generando Tarball para SD: ${NAME_PREFIX}.tar.gz..."
tar -czf "${TARBALL_FILE}" -C "${BOOTFS}" .

# 5. Generar Imagen de disco cruda (.img) con 2 particiones
echo "  Generando Imagen de disco con particiones (Boot ${BOOT_PART_MB:-512}MB + Data ${DATA_PART_MB:-512}MB)..."

BOOT_VFAT="${WORKDIR}/boot.vfat"
DATA_EXT4="${WORKDIR}/data.ext4"
rm -f "$BOOT_VFAT" "$DATA_EXT4" "$IMG_FILE" "$IMG_GZ_FILE"

# Tamaño en bloques de 1K para FAT32 (restando 1MB de offset MBR)
BOOT_BLOCKS=$(( ( ${BOOT_PART_MB:-512} - 1 ) * 1024 ))
# IMPORTANTE: la etiqueta NO puede llamarse "BOOT" porque mtools confunde la etiqueta
# del volumen FAT32 con el directorio boot/ y lo omite silenciosamente (bug de mtools)
FAT_LABEL="${BOOT_LABEL:-APPLIANCE}"
# Asegurar longitud máxima de 11 caracteres para FAT32
FAT_LABEL="${FAT_LABEL:0:11}"

mkfs.vfat -F 32 -S 512 -s 4 -h 2048 -n "$FAT_LABEL" -C "$BOOT_VFAT" $BOOT_BLOCKS >/dev/null

# Crear directorio boot/ explícitamente ANTES de copiar para evitar la colisión
mmd -i "$BOOT_VFAT" ::boot

# Copiar contenido del directorio boot/ primero
mcopy -i "$BOOT_VFAT" -s "${BOOTFS}/boot"/* ::boot/

# Copiar el resto de archivos/directorios de la raíz (excluyendo boot/)
for item in "${BOOTFS}"/*; do
  [ "$(basename "$item")" = "boot" ] && continue
  mcopy -i "$BOOT_VFAT" -s "$item" ::/
done

truncate -s "${DATA_PART_MB:-512}M" "$DATA_EXT4"
mkfs.ext4 -F -L "DATA" "$DATA_EXT4" >/dev/null

TOTAL_SIZE_MB=$(( ${BOOT_PART_MB:-512} + ${DATA_PART_MB:-512} + 1 ))
truncate -s "${TOTAL_SIZE_MB}M" "$IMG_FILE"

parted -s "$IMG_FILE" mklabel msdos
parted -s "$IMG_FILE" mkpart primary fat32 1MiB "${BOOT_PART_MB:-512}MiB"
parted -s "$IMG_FILE" set 1 boot on
parted -s "$IMG_FILE" mkpart primary ext4 "${BOOT_PART_MB:-512}MiB" "$(( ${BOOT_PART_MB:-512} + ${DATA_PART_MB:-512} ))MiB"

dd if="$BOOT_VFAT" of="$IMG_FILE" bs=1M seek=1 conv=notrunc status=none
dd if="$DATA_EXT4" of="$IMG_FILE" bs=1M seek="${BOOT_PART_MB:-512}" conv=notrunc status=none
rm -f "$BOOT_VFAT" "$DATA_EXT4"

echo "  Comprimiendo imagen con gzip..."
gzip -9 -c "$IMG_FILE" > "$IMG_GZ_FILE"

echo -e "\n============================================================"
echo -e "✔ APPLIANCE GENERADO CON ÉXITO:"
echo -e "  1. Tarball SD: ${TARBALL_FILE} ($(du -h "$TARBALL_FILE" | cut -f1))"
echo -e "  2. Imagen SD:  ${IMG_GZ_FILE} ($(du -h "$IMG_GZ_FILE" | cut -f1))"
echo -e "============================================================"
