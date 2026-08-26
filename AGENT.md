# AGENT.md — Knowledge Base & Post-Mortem de Alpine Appliance Builder

> **Propósito de este documento:**  
> Este archivo sirve como memoria técnica y bitácora de arquitectura para futuros agentes de IA y desarrolladores. Recopila todos los problemas críticos descubiertos, sus causas raíz a bajo nivel y las soluciones probadas durante la construcción del appliance inmutable de Alpine Linux para la **Raspberry Pi Zero W (BCM2835 / armv6 / armhf)** y plataformas ARM.

---

## 1. Arquitectura Fundamental de Alpine Linux Diskless

Alpine Linux en modo diskless (appliance inmutable) funciona **100% en memoria RAM (tmpfs)** y carga sus componentes desde la tarjeta SD en el siguiente orden estricto:

```
[GPU VideoCore IV] (bootcode.bin + start.elf)
       │
       ▼ Lee config.txt y usercfg.txt
[Kernel & Initramfs] (vmlinuz-rpi + initramfs-rpi)
       │
       ▼ Monta tmpfs en RAM y ejecuta /init
[Alpine Init Script]
       ├── Monta partición FAT32 en /media/mmcblk0p1
       ├── Instala alpine-base desde /media/mmcblk0p1/apks/armhf/ (requiere APKINDEX firmado)
       ├── Descomprime localhost.apkovl.tar.gz sobre el sysroot
       ├── Monta SquashFS firmado modloop-rpi en /lib/modules
       └── switch_root a /sysroot/sbin/init (OpenRC)
              │
              ▼
       [OpenRC Sysinit -> Boot -> Default Runlevels]
```

### Particionado de la SD
* **Partición 1 (`/dev/mmcblk0p1`):** FAT32 (512 MB) — Solo lectura en tiempo de ejecución. Etiqueta: `P2PT_BOOT` / `APPLIANCE`. Contiene el firmware, kernel, initramfs, modloop y apkovl.
* **Partición 2 (`/dev/mmcblk0p2`):** ext4 (512 MB+) — Lectura/escritura persistente. Montada en `/mnt/data`. Contiene bases de datos locales, certificados, logs y configuraciones de usuario.

---

## 2. Los 9 Errores Críticos y sus Soluciones (Hall of Bugs)

### 🔴 Bug 1: Etiquetas condicionales `[pi0]` en `config.txt`
* **Síntoma:** La GPU VideoCore no cargaba el initramfs o ignoraba directivas de memoria y módulos en ciertas revisiones de Pi Zero W.
* **Causa raíz:** Las secciones condicionales OTP (como `[pi0]` o `[all]`) en `config.txt` dependen del hardware exacto detectado por la GPU. Si la revisión del BCM2835 no coincide con el identificador interno de la GPU, la sección se ignora silenciosamente.
* **Solución:** Usar un `config.txt` plano y canónico, sin secciones condicionales. Añadir siempre `initramfs initramfs-rpi followkernel` para evitar solapamiento de memoria con el kernel. Mantener personalizaciones en `usercfg.txt` mediante `include usercfg.txt`.

---

### 🔴 Bug 2: Colisión de etiqueta FAT32 `"BOOT"` con `mtools/mcopy`
* **Síntoma:** El kernel arrancaba pero lanzaba un panic inmediato:
  ```
  Kernel panic - not syncing: VFS: Unable to mount root fs on unknown-block(0,0)
  ```
* **Causa raíz:** `mcopy` (de `mtools`) en Alpine tiene un bug crítico: si la etiqueta de volumen de la partición FAT32 es `"BOOT"`, al copiar el directorio `boot/`, `mcopy` confunde el directorio con la entrada de etiqueta de volumen del directorio raíz y **omite la copia de todo el directorio `boot/` silenciosamente**. El kernel cargaba desde firmware pero `initramfs-rpi` no existía en el filesystem FAT32.
* **Solución:**
  1. Cambiar la etiqueta del FAT32 a otro nombre (ej. `P2PT_BOOT` o `APPLIANCE`).
  2. Crear explícitamente el directorio con `mmd -i $VFAT ::boot`.
  3. Copiar los contenidos de `boot/*` y de la raíz en dos pasos separados.

---

### 🔴 Bug 3: Regeneración de `APKINDEX.tar.gz` sin firma oficial
* **Síntoma:** El initramfs arrancaba, pero fallaba en el último paso:
  ```
  /sbin/init not found in new root. Launching emergency recovery shell
  ```
* **Causa raíz:** Al ejecutar `apk index` en el paso 1 para indexar paquetes descargados, se sobreescribía el `APKINDEX.tar.gz` original firmado por los desarrolladores de Alpine (`.SIGN.RSA.*`). El script `/init` de Alpine ejecuta `apk add` con verificación estricta de firmas. Al no tener una clave de firma válida, `apk` rechazaba el repositorio y no instalaba `alpine-base`, por lo que `/sbin/init` nunca se creaba en el sysroot.
* **Solución:** **NUNCA regenerar el `APKINDEX.tar.gz` oficial de Alpine**. Mantener el repositorio base con sus paquetes firmados intacto. Los paquetes extra deben descargarse en el primer arranque por red o integrarse en el `apkovl`.

---

### 🔴 Bug 4: Reconstrucción de `modloop-rpi` SquashFS (Pérdida de firma)
* **Síntoma:** El sistema arrancaba pero la tarjeta WiFi no existía:
  ```
  Failed to verify signature of /media/mmcblk0p1/boot/modloop-rpi!
  modprobe: can't change directory to '/lib/modules': No such file or directory
  Could not find a wireless interface
  ```
* **Causa raíz:** Al descomprimir y volver a empaquetar `modloop-rpi` con `mksquashfs` para meter binarios de la aplicación, el archivo SquashFS perdía la firma criptográfica embebida por Alpine. El servicio `/etc/init.d/modloop` verifica la firma RSA antes de montar. Al fallar la verificación, `/lib/modules` quedaba vacío y el driver `brcmfmac` del chip WiFi BCM43438 no se podía cargar.
* **Solución:** **El `modloop-rpi` original de Alpine debe mantenerse 100% inalterado y firmado.** Los binarios custom (`p2pt-server`, etc.) y assets web (`/var/www`) deben ir dentro de `localhost.apkovl.tar.gz`.

---

### 🔴 Bug 5: Orden de Runlevels OpenRC para `hwdrivers` y `modloop`
* **Síntoma:** `modloop` montaba bien, pero seguía saliendo `modprobe: can't change directory to /lib/modules` y no se cargaba el WiFi.
* **Causa raíz:** Cuando hay un archivo `apkovl`, Alpine init no inicializa los runlevels por defecto; hay que crearlos en el apkovl. Si `hwdrivers` se coloca en el runlevel `sysinit`, se ejecuta **antes** de que `modloop` se monte en el runlevel `boot`.
* **Solución:**
  * **Runlevel `sysinit`:** Solo `devfs`, `dmesg`, `mdev`.
  * **Runlevel `boot`:** Orden estricto:
    1. `modloop` (monta `/lib/modules`)
    2. `hwdrivers` (detecta hardware y carga `brcmfmac`)
    3. `wpa_supplicant` (conecta al AP WiFi)
    4. `networking` (asigna IP estática o DHCP)
    5. `appliance-setup` (monta persistencia y aprovisiona servicios)

---

### 🔴 Bug 6: Cuenta de `root` bloqueada y política SSH en Alpine
* **Síntoma:** OpenSSH arrancaba y respondía al puerto 22, pero el login de `root` era rechazado con cualquier contraseña.
* **Causa raíz:**
  1. El paquete `alpine-baselayout` genera `/etc/shadow` con `root:!:...` (cuenta deshabilitada).
  2. La configuración por defecto de OpenSSH en Alpine tiene `PermitRootLogin prohibit-password`.
* **Solución:**
  1. Generar `/etc/shadow` en el `apkovl` con el hash SHA-512 de la contraseña deseada (`openssl passwd -6 "$ROOT_PASSWORD"`).
  2. Inyectar `/etc/ssh/sshd_config` con `PermitRootLogin yes` y `PasswordAuthentication yes`.

---

### 🔴 Bug 7: Conflicto de instancias de `wpa_supplicant` y `dhcpcd`
* **Síntoma:** Fallos intermitentes de conexión WiFi o interfaz `wlan0` no levantando.
* **Causa raíz:** Poner la directiva `wpa-conf /etc/wpa_supplicant/wpa_supplicant.conf` en `/etc/network/interfaces` hacía que `ifup` lanzara una instancia de `wpa_supplicant` que colisionaba con el servicio OpenRC `/etc/init.d/wpa_supplicant`. Además, `dhcpcd` en el runlevel de boot sobrescribía la IP estática.
* **Solución:**
  1. Gestionar `wpa_supplicant` únicamente mediante el servicio OpenRC.
  2. Configurar `/etc/conf.d/wpa_supplicant` con `wpa_supplicant_args="-B -i wlan0 -c /etc/wpa_supplicant/wpa_supplicant.conf -P /run/wpa_supplicant.pid"`.
  3. No incluir `dhcpcd` en el runlevel de boot si se utiliza IP estática vía `/etc/network/interfaces`.

---

### 🔴 Bug 8: Desfase de Reloj (Clock Skew) y Validación SSL/TLS
* **Síntoma:** `apk update` o `curl` fallaban con:
  ```
  SSL routines:tls_post_process_server_certificate:certificate verify failed: Permission denied
  ```
* **Causa raíz:** La Raspberry Pi Zero W **no dispone de reloj en tiempo real con batería (RTC)**. Al arrancar en frío, la fecha del sistema es 1970 o la fecha de compilación de la imagen. Al conectar por HTTPS a los servidores de Alpine (`dl-cdn.alpinelinux.org`), OpenSSL rechaza los certificados TLS porque la fecha actual del sistema es anterior al inicio de validez del certificado.
* **Solución:** En el servicio de primer arranque (`appliance-setup`), antes de ejecutar cualquier comando `apk` o HTTPS, forzar la sincronización horaria mediante NTP en cuanto haya conectividad:
  ```bash
  ntpd -d -n -q -p pool.ntp.org >/dev/null 2>&1 || true
  ```

---

### 🔴 Bug 9: Montaje seguro de almacenamiento persistente (`/mnt/data`)
* **Síntoma:** Al arrancar el appliance sin formatear la segunda partición o en tarjetas SD de menor capacidad, el arranque quedaba bloqueado en modo emergencia de OpenRC.
* **Solución:**
  1. En `/etc/fstab`, configurar `/mnt/data` con la opción `nofail`:
     ```
     /dev/mmcblk0p2  /mnt/data  ext4  defaults,noatime,nofail,errors=remount-ro  0  0
     ```
  2. En el servicio `appliance-setup`, comprobar si `/dev/mmcblk0p2` tiene formato ext4; si no lo tiene, formatearlo automáticamente antes de montar.

---

## 3. Toolkit de Depuración y Comandos Clave

### Guardar cambios en caliente en Alpine Diskless (`lbu`)
Si realizas ajustes manuales en `/etc` en la Raspberry Pi y deseas que persistan entre reinicios:
```bash
mount -o remount,rw /media/mmcblk0p1
LBU_BACKUPDIR=/media/mmcblk0p1 lbu commit -d
cp /media/mmcblk0p1/*.apkovl.tar.gz /media/mmcblk0p1/localhost.apkovl.tar.gz
mount -o remount,ro /media/mmcblk0p1
```
