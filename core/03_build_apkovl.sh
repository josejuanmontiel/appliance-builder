#!/usr/bin/env bash
set -e

# ==============================================================================
# Paso 3: Generar el Overlay de Configuración OpenRC (localhost.apkovl.tar.gz)
# ==============================================================================

WORKDIR="${1:-/workspace/work}"
PAYLOAD_DIR="${2:-/workspace/app-payload}"
CONFIG_ENV="${3:-/workspace/config.env}"

[ -f "$CONFIG_ENV" ] && source "$CONFIG_ENV"

BOOTFS="${WORKDIR}/bootfs"
TMP=$(mktemp -d /tmp/apkovl.XXXXXX)

cleanup() {
    rm -rf "$TMP"
}
trap cleanup EXIT

echo ">> [3/4] Generando overlay de configuración (localhost.apkovl.tar.gz)..."

# 1. Estructura de directorios base
mkdir -p "$TMP"/etc/network \
         "$TMP"/etc/wpa_supplicant \
         "$TMP"/etc/apk \
         "$TMP"/etc/runlevels/sysinit \
         "$TMP"/etc/runlevels/boot \
         "$TMP"/etc/runlevels/default \
         "$TMP"/etc/init.d \
         "$TMP"/etc/conf.d \
         "$TMP"/usr/bin \
         "$TMP"/var/www \
         "$TMP"/root \
         "$TMP"/mnt/data

# 2. Hostname y Resolución DNS
echo "${HOSTNAME:-p2pt-box}" > "$TMP"/etc/hostname

# 2b. Password de root (configurable via ROOT_PASSWORD, default: 'alpine')
ROOT_PASSWORD="${ROOT_PASSWORD:-alpine}"
ROOT_HASH=$(openssl passwd -6 "$ROOT_PASSWORD" 2>/dev/null || \
            echo '$6$rounds=5000$alpine$fakehashreplaced')
mkdir -p "$TMP"/etc "$TMP"/etc/ssh
printf 'root:%s:0:0:99999:7:::\n' "$ROOT_HASH" > "$TMP"/etc/shadow
chmod 640 "$TMP"/etc/shadow
echo "  root password configurado."

# 2c. Configuración SSH para permitir login de root con contraseña
cat <<EOF > "$TMP"/etc/ssh/sshd_config
PermitRootLogin yes
PasswordAuthentication yes
ChallengeResponseAuthentication no
UsePAM no
PrintMotd yes
Subsystem sftp /usr/lib/ssh/sftp-server
EOF
chmod 600 "$TMP"/etc/ssh/sshd_config

cat <<EOF > "$TMP"/etc/hosts
127.0.0.1   localhost localhost.localdomain ${HOSTNAME:-p2pt-box}
::1         localhost localhost.localdomain ${HOSTNAME:-p2pt-box}
${STATIC_IP:-192.168.1.50}  ${HOSTNAME:-p2pt-box}
EOF

cat <<EOF > "$TMP"/etc/resolv.conf
nameserver 1.1.1.1
nameserver 8.8.8.8
EOF

# 3. Interfaces de Red y WiFi
cat <<EOF > "$TMP"/etc/network/interfaces
auto lo
iface lo inet loopback

# eth0 con DHCP (para placas con puerto Ethernet)
auto eth0
iface eth0 inet dhcp

# wlan0 con IP estática o DHCP
# wpa_supplicant lo gestiona el servicio OpenRC
auto wlan0
iface wlan0 inet static
    address ${STATIC_IP:-192.168.1.50}
    netmask ${STATIC_NETMASK:-255.255.255.0}
    gateway ${STATIC_GATEWAY:-192.168.1.1}
EOF

# Configuración del servicio wpa_supplicant OpenRC:
cat <<EOF > "$TMP"/etc/conf.d/wpa_supplicant
wpa_supplicant_args="-B -i wlan0 -c /etc/wpa_supplicant/wpa_supplicant.conf -P /run/wpa_supplicant.pid"
EOF

cat <<EOF > "$TMP"/etc/wpa_supplicant/wpa_supplicant.conf
ctrl_interface=DIR=/var/run/wpa_supplicant GROUP=netdev
update_config=1
country=ES

network={
    ssid="${WIFI_SSID:-YOUR_WIFI_SSID}"
    psk="${WIFI_PASS:-YOUR_WIFI_PASSWORD}"
    key_mgmt=WPA-PSK
}
EOF

# 4. Repositorio de paquetes local
cat <<EOF > "$TMP"/etc/apk/repositories
/media/mmcblk0p1/apks
EOF

# 4b. World file: paquetes base a instalar durante el boot diskless.
cat <<EOF > "$TMP"/etc/apk/world
alpine-base
wpa_supplicant
wpa_supplicant-openrc
openssh-server
openssh-client
dhcpcd
dhcpcd-openrc
EOF

# 5. FSTAB con nofail para permitir arranque en 1 o 2 particiones
cat <<EOF > "$TMP"/etc/fstab
/dev/mmcblk0p1  /media/mmcblk0p1  vfat  ro,noatime  0  0
/dev/mmcblk0p2  /mnt/data         ext4  defaults,noatime,nofail,errors=remount-ro  0  0
EOF

# 6. Configuración de OpenRC
cat <<EOF > "$TMP"/etc/rc.conf
rc_sys=""
rc_cgroup_mode="none"
rc_logger="YES"
rc_log_path="/var/log/rc.log"
unicode="YES"
EOF

# 7. Inyectar servicios custom de la aplicación desde app-payload/services/
if [ -d "${PAYLOAD_DIR}/services" ] && [ "$(ls -A "${PAYLOAD_DIR}/services" 2>/dev/null)" ]; then
    echo "  Inyectando servicios OpenRC personalizados:"
    for srv in "${PAYLOAD_DIR}/services/"*; do
        if [ -f "$srv" ]; then
            srv_name=$(basename "$srv")
            echo "    -> /etc/init.d/${srv_name}"
            cp "$srv" "$TMP/etc/init.d/${srv_name}"
            chmod 755 "$TMP/etc/init.d/${srv_name}"
            ln -sf "/etc/init.d/${srv_name}" "$TMP/etc/runlevels/default/${srv_name}"
        fi
    done
fi

# 8. Inyectar binarios custom desde app-payload/bin/ a /usr/bin/
if [ -d "${PAYLOAD_DIR}/bin" ] && [ "$(ls -A "${PAYLOAD_DIR}/bin" 2>/dev/null)" ]; then
    echo "  Inyectando binarios del payload:"
    for bin in "${PAYLOAD_DIR}/bin/"*; do
        if [ -f "$bin" ] && [ "$(basename "$bin")" != ".gitkeep" ]; then
            echo "    -> /usr/bin/$(basename "$bin")"
            cp "$bin" "$TMP/usr/bin/"
            chmod 755 "$TMP/usr/bin/$(basename "$bin")"
        fi
    done
fi

# 8b. Inyectar assets web desde app-payload/www/ a /var/www/
if [ -d "${PAYLOAD_DIR}/www" ]; then
    for item in "${PAYLOAD_DIR}/www/"*; do
        [ -e "$item" ] || continue
        if [ "$(basename "$item")" != ".gitkeep" ]; then
            echo "  Inyectando asset web: $(basename "$item")"
            cp -r "$item" "$TMP/var/www/"
        fi
    done
fi

# 8c. Inyectar configuraciones personalizadas desde app-payload/etc/
if [ -d "${PAYLOAD_DIR}/etc" ]; then
    for item in "${PAYLOAD_DIR}/etc/"*; do
        [ -e "$item" ] || continue
        if [ "$(basename "$item")" != ".gitkeep" ]; then
            echo "  Inyectando config: $(basename "$item")"
            cp -r "$item" "$TMP/etc/"
        fi
    done
fi

# 8d. Desempaquetar paquetes APK personalizados desde work/custom-apks/
CUSTOM_APKS_DIR="${WORKDIR}/custom-apks"
if [ -d "$CUSTOM_APKS_DIR" ] && [ "$(ls -A "$CUSTOM_APKS_DIR" 2>/dev/null)" ]; then
    echo "  Desempaquetando paquetes APK personalizados en apkovl:"
    for apk in "$CUSTOM_APKS_DIR"/*.apk; do
        if [ -f "$apk" ]; then
            echo "    -> $(basename "$apk")"
            tar -xzf "$apk" -C "$TMP" --exclude='.PKGINFO' --exclude='.SIGN.*' 2>/dev/null || tar -xzf "$apk" -C "$TMP"
        fi
    done
fi

# 8e. Auto-habilitar servicios OpenRC de la aplicación en el runlevel default
for svc in "$TMP"/etc/init.d/*; do
    [ -f "$svc" ] || continue
    svc_name=$(basename "$svc")
    case "$svc_name" in
        appliance-setup|networking|wpa_supplicant|modloop|hwdrivers|devfs|dmesg|mdev|bootmisc|hostname|syslog|swap)
            ;;
        *)
            chmod 755 "$svc" 2>/dev/null || true
            ln -sf "/etc/init.d/${svc_name}" "$TMP/etc/runlevels/default/${svc_name}" 2>/dev/null || true
            ;;
    esac
done

# 9. Runlevels de Alpine: sysinit (CRÍTICO - sin esto modloop no arranca)
for svc in devfs dmesg mdev; do
    ln -sf "/etc/init.d/${svc}" "$TMP"/etc/runlevels/sysinit/${svc} 2>/dev/null || true
done

# Boot runlevel: orden crítico:
#   1. modloop        → monta SquashFS con /lib/modules (brcmfmac, etc.)
#   2. hwdrivers      → detecta hardware y carga módulos (depende de modloop en Alpine)
#   3. wpa_supplicant → se conecta al WiFi (wlan0 ya disponible tras brcmfmac)
#   4. networking     → configura IP
ln -sf /etc/init.d/modloop        "$TMP"/etc/runlevels/boot/modloop        2>/dev/null || true
ln -sf /etc/init.d/hwdrivers      "$TMP"/etc/runlevels/boot/hwdrivers      2>/dev/null || true
ln -sf /etc/init.d/bootmisc       "$TMP"/etc/runlevels/boot/bootmisc       2>/dev/null || true
ln -sf /etc/init.d/hostname       "$TMP"/etc/runlevels/boot/hostname       2>/dev/null || true
ln -sf /etc/init.d/syslog         "$TMP"/etc/runlevels/boot/syslog         2>/dev/null || true
ln -sf /etc/init.d/swap           "$TMP"/etc/runlevels/boot/swap           2>/dev/null || true
ln -sf /etc/init.d/wpa_supplicant "$TMP"/etc/runlevels/boot/wpa_supplicant 2>/dev/null || true
ln -sf /etc/init.d/networking     "$TMP"/etc/runlevels/boot/networking     2>/dev/null || true

# Si existe appliance-setup en init.d, incluirlo en runlevel boot
if [ -f "$TMP"/etc/init.d/appliance-setup ]; then
    ln -sf /etc/init.d/appliance-setup "$TMP"/etc/runlevels/boot/appliance-setup
fi

# Default runlevel: ssh
ln -sf /etc/init.d/sshd "$TMP"/etc/runlevels/default/sshd 2>/dev/null || \
ln -sf /etc/init.d/dropbear "$TMP"/etc/runlevels/default/dropbear 2>/dev/null || true

# 10. Empaquetar localhost.apkovl.tar.gz
TARGET_APKOVL="${BOOTFS}/localhost.apkovl.tar.gz"
tar -czf "$TARGET_APKOVL" -C "$TMP" etc root mnt usr var

echo "✔ localhost.apkovl.tar.gz generado ($(du -h "$TARGET_APKOVL" | cut -f1))"
