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

## 📡 Configuración de Red y WiFi para el Usuario Final

El appliance está diseñado para que cualquier persona pueda configurar la red de forma inmediata sin necesidad de conectar teclado, pantalla ni cables Ethernet a la Raspberry Pi.

```
                                              ┌──► 1. Archivo wifi.txt en la MicroSD (FAT32) [Recomendado]
                                              │
Usuario descarga imagen genérica .img.gz ────┼──► 2. Hotspot WiFi de Rescate (Desde el móvil) [Fallback]
                                              │
                                              └──► 3. En tiempo de build (CI/CD o CLI)
```

### 🟢 1. Modo *Headless Drop-in* en Tarjeta MicroSD (Recomendado)
Es el método más rápido y universal (funciona en Windows, macOS y Linux sin software extra):

1. **Flashear**: Graba la imagen `.img.gz` en tu MicroSD con **Raspberry Pi Imager** o **Balena Etcher**.
2. **Reinsertar en el PC**: Al terminar, saca y vuelve a meter la tarjeta en tu ordenador. Se montará la partición de arranque `P2PT_BOOT` (FAT32).
3. **Crear archivo `wifi.txt`**: Crea un archivo de texto llamado `wifi.txt` en la raíz de la tarjeta:
   ```ini
   SSID=MiRedWiFiDeCasa
   PASS=MiPasswordSuperSegura
   ```
4. *(Opcional)* Si deseas IP estática en lugar de DHCP, crea también `ip.txt`:
   ```ini
   IP=192.168.1.99
   NETMASK=255.255.255.0
   GATEWAY=192.168.1.1
   ```
5. **Arrancar**: Expulsa la tarjeta, métela en la Raspberry Pi y conéctala a la corriente. El servicio `appliance-setup` detectará los archivos en el primer segundo de arranque, aplicará la configuración a `wpa_supplicant` y se conectará automáticamente.

---

### 🔵 2. Modo *Hotspot WiFi de Rescate* (Fallback Automático)
Si la Raspberry Pi arranca y **no detecta ninguna red configurada** (o el router ha cambiado):

1. El sistema conmuta automáticamente a modo **Punto de Acceso Temporal** creando una red WiFi abierta llamada:
   ```
   P2PT-Setup (o <Appliance>-Setup)
   ```
2. Te conectas a esa red desde tu teléfono móvil o portátil.
3. Se abrirá automáticamente el portal de bienvenida en `http://192.168.4.1/` (o accediendo desde el navegador).
4. Selecciona tu red WiFi doméstica de la lista, introduce la contraseña y pulsa **"Guardar y Conectar"**.
5. El appliance guarda las credenciales en el overlay inmutable (`lbu commit`), desactiva el hotspot y se conecta a tu red doméstica.

---

### ⚙️ 3. Configuración en Tiempo de Construcción (CI/CD / CLI)
Si estás generando una imagen para tu propio uso y quieres que ya venga preconfigurada de fábrica:

* **Vía CLI**:
  ```bash
  ./build.sh --wifi-ssid "MiRouter" --wifi-pass "Clave123" --ip "192.168.1.100"
  ```
* **Vía Receta Declarativa (`appliance.yaml`)**:
  ```yaml
  network:
    ip: "192.168.1.50"
    gateway: "192.168.1.1"
  ```
* **Vía Archivo Local (`app-payload/config/wifi.env`)**:
  Copia `wifi.env.example` a `wifi.env` (ignorado por Git por seguridad).

---

## 📦 Enfoque Declarativo: Recetas `appliance.yaml` y Paquetes APK

En lugar de copiar binarios manualmente, puedes definir un appliance de forma puramente declarativa mediante una receta YAML y paquetes `.apk` (generados con herramientas estándar como `nfpm` o paquetes oficiales de Alpine):

### 1. Definir la Receta (`appliance.yaml`)
```yaml
appliance:
  name: "mi-nodo"
  board: "rpi-zero" # rpi-zero | rpi-3 | rpi-4 | generic-x86_64
  hostname: "mi-nodo"

packages:
  # Paquetes oficiales de Alpine Linux:
  alpine:
    - ca-certificates
    - curl
    - caddy

  # Paquetes APK propios (rutas locales o URLs https:// de GitHub Releases):
  apks:
    - ./dist/mi-app_1.0.0_armhf.apk
    # - https://github.com/usuario/repo/releases/download/v1.0.0/mi-app_armhf.apk

services:
  # Servicios OpenRC a habilitar automáticamente:
  enable:
    - mi-app
    - caddy

network:
  ip: "192.168.1.50"
  gateway: "192.168.1.1"
```

### 2. Construir la Imagen desde la Receta
```bash
./build.sh --recipe appliance.yaml
```

---

## 🤖 GitHub Action Reutilizable (CI/CD para cualquier proyecto)

Cualquier repositorio externo puede generar imágenes de appliance automáticamente en sus GitHub Actions usando este builder:

```yaml
# .github/workflows/build-appliance.yml en tu propio repositorio
name: Build Appliance Image

on:
  push:
    tags: ['v*']
  workflow_dispatch:

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      # 1. Compila tu binario y empaqueta el APK con nfpm (o GoReleaser)
      - name: Build APK package
        run: |
          go build -o dist/mi-app .
          nfpm pkg --packager apk --target dist/mi-app_armhf.apk

      # 2. Genera la imagen del Appliance usando esta Action
      - name: Generate Alpine Appliance SD Image
        uses: josejuanmontiel/appliance-builder@v1
        with:
          recipe: appliance.yaml
          apk: dist/mi-app_armhf.apk
          name: mi-appliance

      # 3. Publica los artefactos .img.gz y .tar.gz en tu Release
      - name: Upload SD Image to Release
        uses: softprops/action-gh-release@v2
        with:
          files: |
            output/*.img.gz
            output/*.tar.gz
```

---

## 💾 Salidas Generadas (`output/`)

Al terminar `./build.sh`, obtendrás en la carpeta `output/`:
1. **`<appliance>-rpi.img.gz`**: Imagen de disco en crudo con 2 particiones (Partición 1 FAT32 de arranque de 512MB + Partición 2 ext4 de datos persistentes en `/mnt/data` de 512MB). Grabar directamente con **Raspberry Pi Imager** (*Use Custom*).
2. **`<appliance>-rpi.tar.gz`**: Tarball con los archivos de arranque para descomprimir directamente en una tarjeta SD formateada en FAT32.

---

## 🧠 Base de Conocimiento y Post-Mortem

Consulta [AGENT.md](AGENT.md) para detalles sobre los 9 errores críticos resueltos durante el diseño de la arquitectura (modloop SquashFS firmado, compatibilidad `mtools`, runlevels OpenRC y clock skew en Pi Zero).

