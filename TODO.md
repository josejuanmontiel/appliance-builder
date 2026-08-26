# Alpine Appliance Builder — TODO & Roadmap

> **Estado actual:**  
> Motor base modular listo para empaquetar appliances diskless Alpine en Raspberry Pi.  
> Soporta inyección de payloads desacoplados y configuración de red mediante variables, CLI o archivos drop-in.

---

## 1. 🤖 CI/CD — GitHub Actions para generar imágenes

### Objetivo
Generar automáticamente la imagen `.img.gz` y el tarball `.tar.gz` en cada push a `main` o al crear un tag `v*`, publicándolo como Release Asset.

```yaml
# .github/workflows/build-image.yml
name: Build Alpine Appliance Image

on:
  push:
    tags: ['v*']
  workflow_dispatch:

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up QEMU (ARM emulation)
        uses: docker/setup-qemu-action@v3
        with:
          platforms: arm

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Build Alpine Appliance Image
        run: |
          ./build.sh --name "p2pt-box"

      - name: Upload image as Release Asset
        uses: softprops/action-gh-release@v2
        with:
          files: |
            output/*.img.gz
            output/*.tar.gz
```

- [ ] Crear workflow `.github/workflows/build-image.yml`
- [ ] Configurar caché de Docker builder
- [ ] Publicar checksums SHA256 junto con los artefactos

---

## 2. 🔧 Herramientas de Configuración de Tarjeta SD

### Objetivo
Permitir a los usuarios configurar credenciales WiFi y red estática de forma asistida antes de insertar la tarjeta en la Raspberry Pi.

- [ ] `tools/configure-sd.sh` (Linux / macOS): Script interactivo para montar FAT32 y escribir `wifi.txt` / `ip.txt`.
- [ ] `tools/configure-sd.ps1` (Windows): Script PowerShell para configuración rápida en Windows.
- [ ] `tools/configure-sd-gui.html`: Asistente visual local en navegador para generar los archivos de configuración.

---

## 3. 📶 Modo Hotspot WiFi de Rescate (Captive Portal)

### Objetivo
Si la Raspberry Pi arranca y no consigue asociarse a ninguna red WiFi tras 25 segundos, conmutar temporalmente a modo AP (`hostapd` / `dnsmasq`) sirviendo una página web mínima para elegir red WiFi y guardar con `lbu commit`.

- [ ] Script fallback en `appliance-setup` para timeout de conexión WiFi
- [ ] Mini servidor web de aprovisionamiento en Go/C
- [ ] Persistencia automática en el apkovl (`lbu commit`)

---

## 4. 📦 Nuevas Placas y Arquitecturas

- [ ] Añadir perfiles de kernel/dtb para **Raspberry Pi 4 / Pi 5 (ARM64 / aarch64)**
- [ ] Añadir perfil para **x86_64 UEFI** (Appliance en Mini PC / Servidores locales)
