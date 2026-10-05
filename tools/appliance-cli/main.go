package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultHubURL            = "https://pingo-cloud.accreativos.com"
	defaultDomain            = "appliances.klitosan.com"
	cloudflareOAuthClientID  = "54d11594-84e4-41aa-b438-e81b8fa78ee7" // Client ID público oficial de Cloudflare CLI
	cloudflareOAuthAuthURL   = "https://dash.cloudflare.com/oauth2/auth"
	cloudflareOAuthTokenURL  = "https://dash.cloudflare.com/oauth2/token"
)

func generateSecureToken(length int) string {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("tok_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

func generatePKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
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
		// Fallback manual si el entorno no tiene display
	}
}

func getCLIConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "appliance-cli")
}

func saveSavedCFTitles(token, refreshToken string, expiresAt time.Time) error {
	dir := getCLIConfigDir()
	os.MkdirAll(dir, 0700)
	configPath := filepath.Join(dir, "cloudflare.json")
	data := map[string]any{
		"access_token":  token,
		"refresh_token": refreshToken,
		"expires_at":    expiresAt.Unix(),
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, b, 0600)
}

func loadSavedCFTitles() (string, error) {
	// 1. Probar credenciales guardadas en ~/.config/appliance-cli/cloudflare.json
	cfgPath := filepath.Join(getCLIConfigDir(), "cloudflare.json")
	if b, err := os.ReadFile(cfgPath); err == nil {
		var data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresAt    int64  `json:"expires_at"`
		}
		if json.Unmarshal(b, &data) == nil && data.AccessToken != "" {
			if time.Now().Unix() < data.ExpiresAt {
				return data.AccessToken, nil
			}
			// Token expirado: intentar refresh si existe refresh_token
			if data.RefreshToken != "" {
				newToken, newRefresh, exp, err := refreshCloudflareToken(data.RefreshToken)
				if err == nil {
					_ = saveSavedCFTitles(newToken, newRefresh, exp)
					return newToken, nil
				}
			}
		}
	}

	// 2. Probar credenciales existentes de Wrangler en ~/.config/.wrangler/config/default.toml
	home, _ := os.UserHomeDir()
	wranglerConfig := filepath.Join(home, ".config", ".wrangler", "config", "default.toml")
	if b, err := os.ReadFile(wranglerConfig); err == nil {
		content := string(b)
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "oauth_token =") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					tok := strings.Trim(strings.TrimSpace(parts[1]), "\"")
					if tok != "" {
						return tok, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("no hay sesión activa de Cloudflare")
}

func refreshCloudflareToken(refreshToken string) (string, string, time.Time, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("client_id", cloudflareOAuthClientID)
	data.Set("refresh_token", refreshToken)

	resp, err := http.PostForm(cloudflareOAuthTokenURL, data)
	if err != nil {
		return "", "", time.Time{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", time.Time{}, fmt.Errorf("refresh token fallido (HTTP %d)", resp.StatusCode)
	}

	var res struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", "", time.Time{}, err
	}

	exp := time.Now().Add(time.Duration(res.ExpiresIn) * time.Second)
	return res.AccessToken, res.RefreshToken, exp, nil
}

func printUsage() {
	fmt.Println(`Appliance Cloud Hub CLI — Herramienta de Gestión DDNS, ECH y Cloudflare DNS

Uso:
  appliance-cli <comando> [opciones]

Comandos disponibles:
  cf-login    Inicia sesión directamente en tu cuenta de Cloudflare vía Web (OAuth PKCE)
  cf-dns      Crea o actualiza directamente un registro DNS en Cloudflare (usa sesión o token)
  login       Inicia sesión en Cloud Hub para appliance (OAuth 2.0 Device Flow RFC 8628)
  register    Registra o autoriza un appliance en el Cloud Hub (Cloudflare KV)
  heartbeat   Envía un latido de prueba para verificar conectividad y resolución ECH
  dropin      Genera o actualiza el archivo drop-in ddns.txt para tarjetas SD/FAT32
  help        Muestra esta ayuda

Variables de entorno soportadas:
  CLOUDFLARE_API_TOKEN   Token con permisos Zone.DNS:Edit (opcional si usas 'cf-login')
  CLOUDFLARE_ZONE_ID     ID de la zona DNS en Cloudflare (opcional, se autodetermina)
  HUB_URL                URL del Cloud Hub (Por defecto: https://pingo-cloud.accreativos.com)
  ADMIN_API_KEY          Clave de administración de Cloudflare Worker (opcional)

Ejemplos:
  # 1. Login Web con tu cuenta de Cloudflare (sin copiar ni pegar tokens):
  appliance-cli cf-login

  # 2. Dar de alta subdominio en Cloudflare DNS usando la sesión activa:
  appliance-cli cf-dns -id salon -proxied

  # 3. Provisionar appliance en Cloud Hub mediante Device Code web:
  appliance-cli login -id salon -out ./ddns.txt
`)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "cf-login":
		runCloudflareLogin(os.Args[2:])
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

// runCloudflareLogin inicia el flujo OAuth PKCE oficial de Cloudflare
func runCloudflareLogin(args []string) {
	fs := flag.NewFlagSet("cf-login", flag.ExitOnError)
	portFlag := fs.Int("port", 8976, "Puerto local para recibir callback OAuth")
	noBrowser := fs.Bool("no-browser", false, "No abrir el navegador automáticamente")
	fs.Parse(args)

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *portFlag))
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error abriendo listener en puerto %d: %v\n", *portFlag, err)
		os.Exit(1)
	}
	defer listener.Close()

	verifier, challenge := generatePKCE()
	state := generateSecureToken(16)
	redirectURI := fmt.Sprintf("http://localhost:%d/oauth/callback", *portFlag)

	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", cloudflareOAuthClientID)
	authParams.Set("redirect_uri", redirectURI)
	// Scopes oficiales autorizados para el client_id de Cloudflare CLI
	authParams.Set("scope", "account:read user:read workers:write workers_kv:write workers_routes:write workers_scripts:write zone:read offline_access")
	authParams.Set("state", state)
	authParams.Set("code_challenge", challenge)
	authParams.Set("code_challenge_method", "S256")

	loginURL := fmt.Sprintf("%s?%s", cloudflareOAuthAuthURL, authParams.Encode())

	fmt.Println("==================================================================")
	fmt.Println("           ☁️ INICIO DE SESIÓN EN CLOUDFLARE (OAUTH WEB)          ")
	fmt.Println("==================================================================")
	fmt.Println("Iniciando autorización en tu navegador web...")
	fmt.Printf("Si el navegador no se abre, visita esta URL:\n\033[1;36m%s\033[0m\n", loginURL)
	fmt.Println("------------------------------------------------------------------")

	if !*noBrowser {
		openBrowser(loginURL)
	}

	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	server := &http.Server{}
	http.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		reqState := r.URL.Query().Get("state")
		if reqState != state {
			http.Error(w, "State mismatch", http.StatusBadRequest)
			errChan <- fmt.Errorf("state mismatch en callback")
			return
		}
		authCode := r.URL.Query().Get("code")
		if authCode == "" {
			errMsg := r.URL.Query().Get("error_description")
			if errMsg == "" {
				errMsg = r.URL.Query().Get("error")
			}
			http.Error(w, "Error de autorización: "+errMsg, http.StatusBadRequest)
			errChan <- fmt.Errorf("error de autorización: %s", errMsg)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html><html><body style="font-family:sans-serif;background:#0f172a;color:#f8fafc;display:flex;align-items:center;justify-content:center;height:90vh"><div style="text-align:center;padding:30px;background:#1e293b;border-radius:12px;border:1px solid #334155"><h1 style="color:#38bdf8">✅ ¡Autenticación Completada!</h1><p style="color:#94a3b8">Ya puedes cerrar esta pestaña y volver a tu terminal.</p></div></body></html>`))
		codeChan <- authCode
	})

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	fmt.Println("⏳ Esperando respuesta de autorización de Cloudflare en tu navegador...")

	select {
	case code := <-codeChan:
		_ = server.Close()
		fmt.Println("🔑 Código de autorización recibido. Intercambiando por token...")

		tokenVals := url.Values{}
		tokenVals.Set("grant_type", "authorization_code")
		tokenVals.Set("client_id", cloudflareOAuthClientID)
		tokenVals.Set("code", code)
		tokenVals.Set("redirect_uri", redirectURI)
		tokenVals.Set("code_verifier", verifier)

		tokenResp, err := http.PostForm(cloudflareOAuthTokenURL, tokenVals)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error al canjear token: %v\n", err)
			os.Exit(1)
		}
		defer tokenResp.Body.Close()

		tokenBody, _ := io.ReadAll(tokenResp.Body)
		if tokenResp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "❌ Cloudflare rechazó el intercambio de token (HTTP %d): %s\n", tokenResp.StatusCode, string(tokenBody))
			os.Exit(1)
		}

		var tokData struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int    `json:"expires_in"`
		}
		json.Unmarshal(tokenBody, &tokData)

		exp := time.Now().Add(time.Duration(tokData.ExpiresIn) * time.Second)
		err = saveSavedCFTitles(tokData.AccessToken, tokData.RefreshToken, exp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️ Error guardando credenciales en disco: %v\n", err)
		}

		fmt.Println("\n🎉 ¡Sesión iniciada con éxito en Cloudflare!")
		fmt.Printf("   • Credenciales guardadas en: %s/cloudflare.json\n", getCLIConfigDir())
		fmt.Println("   • Ahora puedes ejecutar 'appliance-cli cf-dns' sin necesidad de pasar ningún token manual.")

	case err := <-errChan:
		_ = server.Close()
		fmt.Fprintf(os.Stderr, "❌ Error en callback: %v\n", err)
		os.Exit(1)
	case <-time.After(120 * time.Second):
		_ = server.Close()
		fmt.Fprintln(os.Stderr, "⌛ Tiempo agotado esperando la autorización web.")
		os.Exit(1)
	}
}

// runCloudflareDNS gestiona directamente registros DNS en Cloudflare
func runCloudflareDNS(args []string) {
	fs := flag.NewFlagSet("cf-dns", flag.ExitOnError)
	idFlag := fs.String("id", "", "ID del appliance (se usará como <id>.appliances.klitosan.com si no se especifica -subdomain)")
	subdomainFlag := fs.String("subdomain", "", "FQDN completo del registro DNS (ej: salon.appliances.klitosan.com)")
	ipFlag := fs.String("ip", "", "Dirección IP pública para el registro A (si se omite, se detectará la IP pública actual)")
	proxiedFlag := fs.Bool("proxied", true, "Habilitar proxy CDN de Cloudflare (activa ECH / TLS automático)")
	cfTokenFlag := fs.String("token", os.Getenv("CLOUDFLARE_API_TOKEN"), "Cloudflare API Token (opcional si ya hiciste 'appliance-cli cf-login')")
	zoneIDFlag := fs.String("zone", os.Getenv("CLOUDFLARE_ZONE_ID"), "Cloudflare Zone ID (o variable CLOUDFLARE_ZONE_ID)")

	fs.Parse(args)

	cfToken := strings.TrimSpace(*cfTokenFlag)
	if cfToken == "" {
		// Intentar cargar token de sesión guardada previamente con cf-login o wrangler
		savedToken, err := loadSavedCFTitles()
		if err == nil && savedToken != "" {
			cfToken = savedToken
			fmt.Println("🔐 Usando sesión activa de Cloudflare.")
		} else {
			fmt.Fprintln(os.Stderr, "❌ Error: No se encontró sesión ni token de Cloudflare.")
			fmt.Fprintln(os.Stderr, "   👉 Inicia sesión con: appliance-cli cf-login")
			fmt.Fprintln(os.Stderr, "   👉 O define la variable: export CLOUDFLARE_API_TOKEN=...")
			os.Exit(1)
		}
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
