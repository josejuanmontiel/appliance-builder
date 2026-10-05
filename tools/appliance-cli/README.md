# Appliance Cloud Hub CLI (`appliance-cli`)

Herramienta en Go para aprovisionar, registrar y gestionar subdominios y credenciales para Appliances de Pingo y EstoyQueLoLeo con soporte ECH.

## Compilación

```bash
cd tools/appliance-cli
go build -o appliance-cli main.go
```

## Modos de Uso

### 1. Dar de alta subdominios directamente en Cloudflare DNS (`cf-dns`)

Permite crear o actualizar registros DNS tipo `A` en Cloudflare con o sin Proxy CDN (para habilitar TLS y ECH automáticamente):

```bash
export CLOUDFLARE_API_TOKEN="tu-token-cloudflare"
# Opcional: CLOUDFLARE_ZONE_ID (se autodetermina si se omite)

# Crear subdominio (autodetectando la IP pública WAN):
./appliance-cli cf-dns -id salon -proxied

# Crear o actualizar especificando subdominio e IP manual:
./appliance-cli cf-dns -subdomain salon.appliances.klitosan.com -ip 85.12.34.56 -proxied
```

### 2. Aprovisionamiento vía Web sin tokens manuales (`login`)

Usa el flujo OAuth 2.0 Device Flow (RFC 8628):

```bash
./appliance-cli login -id salon -out ./ddns.txt
```
1. El CLI solicita el código al Cloud Hub.
2. Abre automáticamente el navegador en `https://pingo-cloud.accreativos.com/ddns/activate?code=ABCD-1234`.
3. Pulsas **"Autorizar Dispositivo"** en la web.
4. El CLI recibe las credenciales y escribe directamente el archivo de arranque `ddns.txt`.

### 3. Registro manual en Cloud Hub (`register`)

```bash
./appliance-cli register -id salon -out ./ddns.txt
```

### 4. Probar conectividad y estado ECH (`heartbeat`)

```bash
./appliance-cli heartbeat -id salon -token <token>
```
