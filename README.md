# 🏔️ Alpine Appliance Builder

Generador genérico y modular de **Appliances Inmutables (Live / Diskless)** basados en **Alpine Linux** para sistemas embebidos (Raspberry Pi Zero, Zero W, Pi 3, Pi 4 y x86_64).

Permite empaquetar cualquier conjunto de aplicaciones compiladas (Go, Rust, C) y frontends estáticos en una imagen de disco autónoma, inmutable y resistente a cortes de energía.

Por defecto, incluye como ejemplo de referencia **[P2PT Server](https://estoyqueloleo-max.github.io/p2pt)** (servidor autónomo de señalización WebRTC PeerJS + STUN/TURN pion en Go).

---

## 📁 Estructura del Proyecto

El repositorio desacopla completamente el **motor de arranque Alpine** del **payload de la aplicación**:

```
appliance-builder/
├── build.sh                         # CLI principal de construcción
├── Dockerfile.builder               # Entorno Docker hermético para compilar
├── config.default.env               # Configuración global del appliance
├── core/                            # Motor agnóstico de Alpine
│   ├── 01_fetch_alpine_base.sh      # Descarga y valida el release oficial de Alpine
│   ├── 02_build_modloop.sh          # Verifica modloop SquashFS firmado
│   ├── 03_build_apkovl.sh           # Genera overlay OpenRC (red, wifi, servicios, binarios)
│   └── 04_build_disk_image.sh       # Ensambla imagen particionada (.img.gz y .tar.gz)
├── app-payload/                     # 📦 PAYLOAD DE EJEMPLO (P2PT Server)
│   ├── bin/                         # Binarios ejecutables (ej. p2pt-server)
│   ├── www/                         # Frontend Web SPA o assets estáticos
│   ├── services/                    # Scripts OpenRC para /etc/init.d/ (ej. p2pt, appliance-setup)
│   ├── etc/                         # Configuraciones personalizadas (/etc/...)
│   └── config/
│       ├── extra-apks.txt           # Paquetes APK adicionales del repositorio
│       ├── wifi.env.example         # Plantilla de configuración de red
│       └── p2pt.env.example         # Plantilla de variables de entorno de la app
├── emulator/                        # Simuladores locales (Docker y QEMU)
└── output/                          # Artefactos finales generados (.img.gz y .tar.gz)
```

---

## 🚀 Uso Rápido

### 1. Construir con parámetros por defecto
```bash
./build.sh
```

### 2. Construir personalizando Red y WiFi mediante CLI
```bash
./build.sh \
  --name "mi-nodo" \
  --wifi-ssid "MiRouterWiFi" \
  --wifi-pass "MiPasswordSuperSegura" \
  --ip "192.168.1.100" \
  --gateway "192.168.1.1"
```

### 3. Inyectar un Payload Personalizado externo
Puedes apuntar a cualquier directorio de payload sin modificar este repositorio:
```bash
./build.sh --payload /ruta/a/mi-app-payload --name mi-appliance
```

### 4. Opciones disponibles en CLI (`./build.sh --help`)
| Opción | Descripción | Valor por defecto |
| :--- | :--- | :--- |
| `--payload` | Directorio de payload personalizado | `./app-payload` |
| `--wifi-ssid` | Nombre de la red WiFi | *(Definido en config)* |
| `--wifi-pass` | Contraseña de la red WiFi | *(Oculta)* |
| `--ip` | Dirección IP estática para `wlan0` | `192.168.1.50` |
| `--netmask` | Máscara de subred | `255.255.255.0` |
| `--gateway` | Puerta de enlace predeterminada | `192.168.1.1` |
| `--root-pass` | Contraseña del usuario root | `alpine` |
| `--name` | Nombre prefijo de la imagen generada | `p2pt-box` |
| `--rebuild` | Recompila el contenedor Docker del builder | `false` |
| `--no-docker` | Ejecuta nativo en el host (solo en Alpine) | `false` |

---

## 📡 ¿Dónde se configura la Red y la WiFi?

Tienes **3 niveles** de configuración:

### Nivel 1: En tiempo de construcción (Ficheros de Configuración)
Copia `app-payload/config/wifi.env.example` a `app-payload/config/wifi.env`:
```bash
WIFI_SSID="MiRedWiFi"
WIFI_PASS="ClaveWiFi123"
STATIC_IP="192.168.1.50"
STATIC_GATEWAY="192.168.1.1"
```

### Nivel 2: En tiempo de compilación (Flags CLI)
Pásalo directamente como argumentos a `./build.sh --wifi-ssid "..." --wifi-pass "..." --ip "..."`.

### Nivel 3: En caliente desde la tarjeta SD (Modo *Headless Drop-in*)
Una vez grabada la MicroSD, puedes meterla en cualquier PC (Windows/Mac/Linux) y crear en la raíz de la partición de arranque (FAT32):
* **`wifi.txt`**:
  ```ini
  SSID=NuevaRedWiFi
  PASS=NuevaClave
  ```
* **`ip.txt`**:
  ```ini
  IP=192.168.1.99
  NETMASK=255.255.255.0
  GATEWAY=192.168.1.1
  ```
*Al encender la Raspberry Pi, el servicio `appliance-setup` detectará estos archivos y aplicará la configuración al vuelo.*

---

## 🐹 Guía: Cómo compilar e inyectar tu propio aplicativo

Cualquier aplicación compilada estáticamente puede convertirse en un appliance inmutable en 4 pasos:

### 1. Compilación estática cruzada (Ejemplo Go)
```bash
# Para Raspberry Pi Zero / Zero W (ARMv6 de 32 bits):
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build -ldflags="-s -w" -o app-payload/bin/mi-app ./cmd/server

# Para Raspberry Pi 2 / 3 / 4 (ARMv7 32 bits):
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" -o app-payload/bin/mi-app ./cmd/server

# Para Raspberry Pi 3 / 4 / 5 (ARM64 64 bits):
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o app-payload/bin/mi-app ./cmd/server
```

### 2. Crear el script de servicio OpenRC (`app-payload/services/mi-app`)
```bash
#!/sbin/openrc-run
description="Mi Aplicación Daemon"

command="/usr/bin/mi-app"
command_args="${MI_APP_OPTS:---port 8080}"
command_user="root"
command_background="yes"
pidfile="/run/mi-app.pid"

depend() {
    need net
    after wpa_supplicant networking appliance-setup
}
```

### 3. Paquetes del sistema opcionales (`app-payload/config/extra-apks.txt`)
Añade los paquetes de Alpine que requieras (ej. `ca-certificates`, `curl`, `sqlite`).

### 4. Construir la imagen
```bash
./build.sh --name "mi-app"
```

---

## 💾 Salidas Generadas (`output/`)

Al terminar `./build.sh`, obtendrás en la carpeta `output/`:
1. **`<appliance>-rpi.img.gz`**: Imagen de disco en crudo con 2 particiones (Partición 1 FAT32 de arranque de 512MB + Partición 2 ext4 de datos persistentes en `/mnt/data` de 512MB). Grabar directamente con **Raspberry Pi Imager** (*Use Custom*).
2. **`<appliance>-rpi.tar.gz`**: Tarball con los archivos de arranque para descomprimir directamente en una tarjeta SD formateada en FAT32.

---

## 🧠 Base de Conocimiento y Post-Mortem

Consulta [AGENT.md](AGENT.md) para detalles sobre los 9 errores críticos resueltos durante el diseño de la arquitectura (modloop SquashFS firmado, compatibilidad `mtools`, runlevels OpenRC y clock skew en Pi Zero).
