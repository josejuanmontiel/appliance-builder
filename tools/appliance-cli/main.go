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
	"path/filepath"
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
		// Fallback simple timestamp based pseudo-random
		return fmt.Sprintf("tok_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

func printUsage() {
	fmt.Println(`Appliance Cloud Hub CLI — Herramienta de Gestión DDNS / ECH

Uso:
  appliance-cli <comando> [opciones]

Comandos disponibles:
  register    Registra o autoriza un appliance en el Cloud Hub (Cloudflare KV)
  heartbeat   Envía un latido de prueba para verificar conectividad y resolución ECH
  dropin      Genera o actualiza el archivo drop-in ddns.txt para tarjetas SD/FAT32
  help        Muestra esta ayuda

Variables de entorno soportadas:
  HUB_URL        URL del Cloud Hub (Por defecto: https://pingo-cloud.accreativos.com)
  ADMIN_API_KEY  Clave de administración de Cloudflare Worker (opcional)

Ejemplos:
  appliance-cli register -id nodo-oficina -out ./ddns.txt
  appliance-cli heartbeat -id nodo-oficina -token <token>
  appliance-cli dropin -id nodo-oficina -token <token> -out /media/sdcard/ddns.txt
`)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
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

	// Generar archivo drop-in si se especificó -out
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
