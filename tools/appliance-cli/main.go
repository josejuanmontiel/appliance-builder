package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultHubURL = "https://pingo-cloud.accreativos.com"
	defaultDomain = "appliances.klitosan.com"
)

func generateSecureToken(length int) string {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("tok_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("sistema operativo no soportado para apertura automática")
	}
	if err != nil {
		// No es un fallo crítico, el usuario puede abrir la URL manualmente
	}
}

func printUsage() {
	fmt.Println(`Appliance Cloud Hub CLI — Herramienta de Gestión DDNS, ECH y Cloudflare DNS

Uso:
  appliance-cli <comando> [opciones]

Comandos disponibles:
  login       Inicia sesión vía Web (OAuth 2.0 Device Flow RFC 8628) sin tokens manuales
  cf-dns      Crea o actualiza directamente un registro DNS (A o CNAME) en la API de Cloudflare
  register    Registra o autoriza un appliance en el Cloud Hub (Cloudflare KV)
  heartbeat   Envía un latido de prueba para verificar conectividad y resolución ECH
  dropin      Genera o actualiza el archivo drop-in ddns.txt para tarjetas SD/FAT32
  help        Muestra esta ayuda

Variables de entorno soportadas:
  CLOUDFLARE_API_TOKEN   Token con permisos Zone.DNS:Edit para gestionar registros DNS
  CLOUDFLARE_ZONE_ID     ID de la zona DNS en Cloudflare (ej. para klitosan.com)
  HUB_URL                URL del Cloud Hub (Por defecto: https://pingo-cloud.accreativos.com)
  ADMIN_API_KEY          Clave de administración de Cloudflare Worker (opcional)

Ejemplos:
  # 1. Crear subdominio directamente en Cloudflare DNS:
  appliance-cli cf-dns -subdomain salon.appliances.klitosan.com -ip 1.2.3.4 -proxied
  appliance-cli cf-dns -id oficina -ip 85.12.34.56

  # 2. Login interactivo para aprovisionar appliance:
  appliance-cli login -id salon -out ./ddns.txt

  # 3. Comprobación de heartbeat:
  appliance-cli heartbeat -id salon -token <token>
`)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "cf-dns":
		runCloudflareDNS(os.Args[2:])
	case "login":
		runLogin(os.Args[2:])
	case "register":
		runRegister(os.Args[2:])
	case "heartbeat":
		runHeartbeat(os.Args[2:])
	case "dropin":
		runDropin(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Error: comando desconocido '%s'\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func getEnvOrDefault(envKey, defVal string) string {
	v := os.Getenv(envKey)
	if v != "" {
		return v
	}
	return defVal
}

// runCloudflareDNS permite gestionar directamente registros DNS en Cloudflare sin intermediarios
func runCloudflareDNS(args []string) {
	fs := flag.NewFlagSet("cf-dns", flag.ExitOnError)
	idFlag := fs.String("id", "", "ID del appliance (se usará como <id>.appliances.klitosan.com si no se especifica -subdomain)")
	subdomainFlag := fs.String("subdomain", "", "FQDN completo del registro DNS (ej: salon.appliances.klitosan.com)")
	ipFlag := fs.String("ip", "", "Dirección IP pública para el registro A (si se omite, se detectará la IP pública actual)")
	proxiedFlag := fs.Bool("proxied", true, "Habilitar proxy CDN de Cloudflare (activa ECH / TLS automático)")
	cfTokenFlag := fs.String("token", os.Getenv("CLOUDFLARE_API_TOKEN"), "Cloudflare API Token (o variable CLOUDFLARE_API_TOKEN)")
	zoneIDFlag := fs.String("zone", os.Getenv("CLOUDFLARE_ZONE_ID"), "Cloudflare Zone ID (o variable CLOUDFLARE_ZONE_ID)")

	fs.Parse(args)

	cfToken := strings.TrimSpace(*cfTokenFlag)
	if cfToken == "" {
		fmt.Fprintln(os.Stderr, "❌ Error: Se requiere CLOUDFLARE_API_TOKEN (-token o variable de entorno).")
		os.Exit(1)
	}

	subdomain := strings.TrimSpace(*subdomainFlag)
	if subdomain == "" {
		if *idFlag != "" {
			subdomain = fmt.Sprintf("%s.%s", strings.ToLower(strings.TrimSpace(*idFlag)), defaultDomain)
		} else {
			fmt.Fprintln(os.Stderr, "❌ Error: Especifica -subdomain o -id.")
			os.Exit(1)
		}
	}

	zoneID := strings.TrimSpace(*zoneIDFlag)
	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Si no se pasa Zone ID, buscarlo automáticamente a través de la API
	if zoneID == "" {
		fmt.Println("🔍 Buscando Zone ID para el dominio en Cloudflare...")
		detectedZoneID, err := detectZoneID(client, cfToken, subdomain)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error localizando Zone ID: %v\n", err)
			os.Exit(1)
		}
		zoneID = detectedZoneID
		fmt.Printf("   • Zone ID detectado: %s\n", zoneID)
	}

	// 2. Determinar IP pública (si no se suministró)
	targetIP := strings.TrimSpace(*ipFlag)
	if targetIP == "" {
		fmt.Println("🌐 Detectando IP pública de esta conexión...")
		publicIP, err := detectPublicIP(client)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error obteniendo IP pública actual: %v. Especifica -ip manualmente.\n", err)
			os.Exit(1)
		}
		targetIP = publicIP
		fmt.Printf("   • IP WAN detectada: %s\n", targetIP)
	}

	// 3. Comprobar si el registro DNS ya existe en la zona
	fmt.Printf("🔍 Consultando registros DNS existentes para '%s'...\n", subdomain)
	existingRecordID, err := findDNSRecord(client, cfToken, zoneID, subdomain)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error consultando DNS de Cloudflare: %v\n", err)
		os.Exit(1)
	}

	// 4. Crear o Actualizar el registro DNS
	dnsPayload := map[string]any{
		"type":    "A",
		"name":    subdomain,
		"content": targetIP,
		"ttl":     1, // Auto
		"proxied": *proxiedFlag,
	}
	payloadBytes, _ := json.Marshal(dnsPayload)

	var apiURL, httpMethod string
	if existingRecordID == "" {
		apiURL = fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID)
		httpMethod = http.MethodPost
		fmt.Printf("➕ Creando nuevo registro DNS A -> %s en Cloudflare...\n", subdomain)
	} else {
		apiURL = fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", zoneID, existingRecordID)
		httpMethod = http.MethodPut
		fmt.Printf("🔄 Actualizando registro DNS existente (%s) -> %s...\n", existingRecordID, subdomain)
	}

	req, _ := http.NewRequest(httpMethod, apiURL, bytes.NewBuffer(payloadBytes))
	req.Header.Set("Authorization", "Bearer "+cfToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error conectando con API de Cloudflare: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var cfResp struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Content string `json:"content"`
			Proxied bool   `json:"proxied"`
		} `json:"result"`
	}
	json.Unmarshal(bodyBytes, &cfResp)

	if !cfResp.Success {
		var errMsgs []string
		for _, e := range cfResp.Errors {
			errMsgs = append(errMsgs, fmt.Sprintf("[%d] %s", e.Code, e.Message))
		}
		fmt.Fprintf(os.Stderr, "❌ Cloudflare API Error: %s\n", strings.Join(errMsgs, ", "))
		os.Exit(1)
	}

	fmt.Println("\n🎉 ¡Subdominio configurado con éxito en Cloudflare DNS!")
	fmt.Printf("   • Subdominio  : %s\n", cfResp.Result.Name)
	fmt.Printf("   • Dirección IP: %s\n", cfResp.Result.Content)
	fmt.Printf("   • Proxied     : %v\n", cfResp.Result.Proxied)
	if cfResp.Result.Proxied {
		fmt.Println("   • ECH Ready   : ✅ Sí (TLS y Proxy Cloudflare activos)")
	}
	fmt.Printf("   • DNS RecordID: %s\n", cfResp.Result.ID)
}

func detectZoneID(client *http.Client, token, subdomain string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, "https://api.cloudflare.com/client/v4/zones?status=active", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var zonesResp struct {
		Success bool `json:"success"`
		Result  []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&zonesResp); err != nil {
		return "", err
	}

	// Buscar la zona que coincida con el sufijo del subdominio
	for _, z := range zonesResp.Result {
		if strings.HasSuffix(subdomain, z.Name) {
			return z.ID, nil
		}
	}

	if len(zonesResp.Result) > 0 {
		return zonesResp.Result[0].ID, nil
	}
	return "", fmt.Errorf("no se encontraron zonas DNS asociadas a este token")
}

func detectPublicIP(client *http.Client) (string, error) {
	endpoints := []string{
		"https://api.ipify.org",
		"https://icanhazip.com",
		"https://cloudflare.com/cdn-cgi/trace",
	}

	for _, ep := range endpoints {
		resp, err := client.Get(ep)
		if err != nil {
			continue
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err == nil {
			str := string(b)
			if strings.Contains(ep, "trace") {
				for _, line := range strings.Split(str, "\n") {
					if strings.HasPrefix(line, "ip=") {
						return strings.TrimPrefix(line, "ip="), nil
					}
				}
			} else {
				ip := strings.TrimSpace(str)
				if ip != "" {
					return ip, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no se pudo determinar la IP pública")
}

func findDNSRecord(client *http.Client, token, zoneID, name string) (string, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?name=%s&type=A", zoneID, name)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var listResp struct {
		Success bool `json:"success"`
		Result  []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return "", err
	}

	for _, r := range listResp.Result {
		if strings.EqualFold(r.Name, name) {
			return r.ID, nil
		}
	}
	return "", nil
}

func runLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	idFlag := fs.String("id", "", "ID único del appliance a autorizar (ej: salon, oficina, nodo-01)")
	subdomainFlag := fs.String("subdomain", "", "Subdominio FQDN deseado (opcional, ej: salon.appliances.klitosan.com)")
	hubFlag := fs.String("hub", getEnvOrDefault("HUB_URL", defaultHubURL), "URL base del Cloud Hub")
	outFlag := fs.String("out", "ddns.txt", "Ruta para escribir el archivo drop-in ddns.txt generado")
	noBrowser := fs.Bool("no-browser", false, "No abrir automáticamente el navegador web")

	fs.Parse(args)

	if *idFlag == "" {
		fmt.Fprintln(os.Stderr, "❌ Error: El parámetro -id es obligatorio (ej: appliance-cli login -id nodo-salon).")
		fs.Usage()
		os.Exit(1)
	}

	applianceID := strings.TrimSpace(*idFlag)
	subdomain := strings.TrimSpace(*subdomainFlag)
	if subdomain == "" {
		subdomain = fmt.Sprintf("%s.%s", strings.ToLower(applianceID), defaultDomain)
	}

	hubURL := strings.TrimRight(*hubFlag, "/")
	deviceCodeEndpoint := hubURL + "/api/v1/ddns/device-code"

	fmt.Println("🌐 Solicitando autorización de dispositivo al Cloud Hub...")

	reqBody, _ := json.Marshal(map[string]string{
		"applianceId": applianceID,
		"subdomain":   subdomain,
	})

	resp, err := http.Post(deviceCodeEndpoint, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error conectando con el Cloud Hub (%s): %v\n", deviceCodeEndpoint, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "❌ Error iniciando flujo de autorización (HTTP %d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var dCodeResp struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dCodeResp); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error decodificando respuesta de autorización: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n==================================================================")
	fmt.Println("             🔐 AUTORIZACIÓN VÍA WEB (DEVICE FLOW)               ")
	fmt.Println("==================================================================")
	fmt.Printf(" Appliance ID : %s\n", applianceID)
	fmt.Printf(" Subdominio   : %s\n", subdomain)
	fmt.Println("------------------------------------------------------------------")
	fmt.Printf(" 1. Visita la URL: \033[1;36m%s\033[0m\n", dCodeResp.VerificationURIComplete)
	fmt.Printf(" 2. Confirma el código: \033[1;33m%s\033[0m\n", dCodeResp.UserCode)
	fmt.Println("==================================================================")

	if !*noBrowser {
		fmt.Println("🚀 Abriendo el navegador web automáticamente...")
		openBrowser(dCodeResp.VerificationURIComplete)
	}

	fmt.Println("\n⏳ Esperando a que apruebes la autorización en la web...")

	tokenEndpoint := hubURL + "/api/v1/ddns/device-token"
	interval := time.Duration(dCodeResp.Interval) * time.Second
	if interval < 2*time.Second {
		interval = 2 * time.Second
	}

	deadline := time.Now().Add(time.Duration(dCodeResp.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(interval)

		pollBody, _ := json.Marshal(map[string]string{
			"device_code": dCodeResp.DeviceCode,
		})

		pollResp, err := http.Post(tokenEndpoint, "application/json", bytes.NewBuffer(pollBody))
		if err != nil {
			continue
		}

		respBytes, _ := io.ReadAll(pollResp.Body)
		pollResp.Body.Close()

		if pollResp.StatusCode == http.StatusOK {
			var tokenData struct {
				ApplianceID string `json:"applianceId"`
				Subdomain   string `json:"subdomain"`
				SecretToken string `json:"secret_token"`
			}
			json.Unmarshal(respBytes, &tokenData)

			fmt.Println("\n🎉 ¡Autorización completada con éxito desde la Web!")
			fmt.Printf("   • Appliance ID : %s\n", tokenData.ApplianceID)
			fmt.Printf("   • Subdominio   : %s\n", tokenData.Subdomain)
			fmt.Printf("   • Token Secreto: %s (recibido y protegido)\n", tokenData.SecretToken)

			if *outFlag != "" {
				err := writeDropinFile(*outFlag, tokenData.ApplianceID, tokenData.SecretToken, hubURL)
				if err != nil {
					fmt.Fprintf(os.Stderr, "⚠️ Error guardando archivo drop-in: %v\n", err)
				} else {
					fmt.Printf("📄 Archivo drop-in generado: %s\n", *outFlag)
				}
			}
			return
		}

		var errData struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		json.Unmarshal(respBytes, &errData)

		if errData.Error == "authorization_pending" {
			fmt.Print(".")
			continue
		}

		if errData.Error != "" {
			fmt.Fprintf(os.Stderr, "\n❌ Autorización finalizada con error: %s (%s)\n", errData.Error, errData.ErrorDescription)
			os.Exit(1)
		}
	}

	fmt.Fprintln(os.Stderr, "\n⌛ Tiempo agotado: El código de dispositivo ha expirado.")
	os.Exit(1)
}

func runRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	idFlag := fs.String("id", "", "ID único del appliance (ej: salon, oficina, nodo-01)")
	tokenFlag := fs.String("token", "", "Token secreto (opcional, si se omite se generará automáticamente)")
	subdomainFlag := fs.String("subdomain", "", "Subdominio FQDN personalizado (opcional, ej: oficina.appliances.klitosan.com)")
	hubFlag := fs.String("hub", getEnvOrDefault("HUB_URL", defaultHubURL), "URL base del Cloud Hub")
	adminKeyFlag := fs.String("admin-key", os.Getenv("ADMIN_API_KEY"), "Clave administrativa del Cloud Hub (X-Admin-Key)")
	outFlag := fs.String("out", "", "Ruta para escribir el archivo drop-in ddns.txt generado")

	fs.Parse(args)

	if *idFlag == "" {
		fmt.Fprintln(os.Stderr, "❌ Error: El parámetro -id es obligatorio.")
		fs.Usage()
		os.Exit(1)
	}

	applianceID := strings.TrimSpace(*idFlag)
	token := strings.TrimSpace(*tokenFlag)
	if token == "" {
		token = generateSecureToken(16)
		fmt.Printf("🔑 Token autogenerado para '%s': %s\n", applianceID, token)
	}

	subdomain := strings.TrimSpace(*subdomainFlag)
	if subdomain == "" {
		subdomain = fmt.Sprintf("%s.%s", strings.ToLower(applianceID), defaultDomain)
	}

	registerEndpoint := strings.TrimRight(*hubFlag, "/") + "/api/v1/ddns/register"

	payload := map[string]string{
		"applianceId": applianceID,
		"secretToken": token,
		"subdomain":   subdomain,
	}

	bodyBytes, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, registerEndpoint, bytes.NewBuffer(bodyBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error creando solicitud HTTP: %v\n", err)
		os.Exit(1)
	}

	req.Header.Set("Content-Type", "application/json")
	if *adminKeyFlag != "" {
		req.Header.Set("X-Admin-Key", *adminKeyFlag)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error al conectar con el Cloud Hub (%s): %v\n", registerEndpoint, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "❌ Registro fallido (HTTP %d): %s\n", resp.StatusCode, string(respBytes))
		os.Exit(1)
	}

	var resData map[string]any
	json.Unmarshal(respBytes, &resData)

	fmt.Println("✅ ¡Appliance registrado exitosamente en Cloud Hub!")
	fmt.Printf("   • Appliance ID : %s\n", applianceID)
	fmt.Printf("   • Subdominio   : %s\n", subdomain)
	fmt.Printf("   • Token Secreto: %s\n", token)
	fmt.Printf("   • Cloud Hub    : %s\n", *hubFlag)

	if *outFlag != "" {
		err := writeDropinFile(*outFlag, applianceID, token, *hubFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️ Error guardando archivo drop-in: %v\n", err)
		} else {
			fmt.Printf("📄 Archivo drop-in generado: %s\n", *outFlag)
		}
	} else {
		fmt.Println("\n💡 Para configurar el arranque del appliance, puedes crear un archivo 'ddns.txt' con:")
		fmt.Println("----------------------------------------")
		fmt.Printf("ID=%s\nTOKEN=%s\nHUB=%s\n", applianceID, token, *hubFlag)
		fmt.Println("----------------------------------------")
	}
}

func runHeartbeat(args []string) {
	fs := flag.NewFlagSet("heartbeat", flag.ExitOnError)
	idFlag := fs.String("id", "", "ID del appliance")
	tokenFlag := fs.String("token", "", "Token secreto del appliance")
	hubFlag := fs.String("hub", getEnvOrDefault("HUB_URL", defaultHubURL), "URL base del Cloud Hub")

	fs.Parse(args)

	if *idFlag == "" || *tokenFlag == "" {
		fmt.Fprintln(os.Stderr, "❌ Error: -id y -token son obligatorios.")
		fs.Usage()
		os.Exit(1)
	}

	heartbeatEndpoint := strings.TrimRight(*hubFlag, "/") + "/api/v1/ddns/heartbeat"

	req, err := http.NewRequest(http.MethodPost, heartbeatEndpoint, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error creando solicitud HTTP: %v\n", err)
		os.Exit(1)
	}

	req.Header.Set("X-Appliance-ID", *idFlag)
	req.Header.Set("Authorization", "Bearer "+*tokenFlag)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error de conexión con Cloud Hub: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "❌ Heartbeat rechazado (HTTP %d): %s\n", resp.StatusCode, string(respBytes))
		os.Exit(1)
	}

	var resData map[string]any
	json.Unmarshal(respBytes, &resData)

	fmt.Println("💓 ¡Heartbeat recibido y procesado por Cloud Hub!")
	fmt.Printf("   • Estado    : %v\n", resData["status"])
	fmt.Printf("   • IP WAN    : %v\n", resData["ip"])
	fmt.Printf("   • Subdominio: %v\n", resData["subdomain"])
	if ech, ok := resData["echReady"].(bool); ok && ech {
		fmt.Println("   • ECH Ready : ✅ Sí (Proxy y TLS Cloudflare activos)")
	}
}

func runDropin(args []string) {
	fs := flag.NewFlagSet("dropin", flag.ExitOnError)
	idFlag := fs.String("id", "", "ID del appliance")
	tokenFlag := fs.String("token", "", "Token secreto del appliance")
	hubFlag := fs.String("hub", getEnvOrDefault("HUB_URL", defaultHubURL), "URL base del Cloud Hub")
	outFlag := fs.String("out", "ddns.txt", "Ruta del archivo de salida")

	fs.Parse(args)

	if *idFlag == "" || *tokenFlag == "" {
		fmt.Fprintln(os.Stderr, "❌ Error: -id y -token son obligatorios.")
		fs.Usage()
		os.Exit(1)
	}

	err := writeDropinFile(*outFlag, *idFlag, *tokenFlag, *hubFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error escribiendo archivo drop-in: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Archivo drop-in escrito con éxito en: %s\n", *outFlag)
}

func writeDropinFile(filePath, id, token, hub string) error {
	dir := filepath.Dir(filePath)
	if dir != "" && dir != "." {
		os.MkdirAll(dir, 0755)
	}

	content := fmt.Sprintf("# Configuración DDNS / Cloud Hub para Appliance\nID=%s\nTOKEN=%s\nHUB=%s\n", id, token, hub)
	return os.WriteFile(filePath, []byte(content), 0644)
}
