package main

import (
	"fmt"
	"os"
	"strings"
)

func readTrimmed(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func envOrFile(keys, paths []string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	for _, path := range paths {
		if value := readTrimmed(path); value != "" {
			return value
		}
	}
	return ""
}

func sandboxID() string {
	return envOrFile(
		[]string{"E2B_SANDBOX_ID", "SUPERCHARGE_SANDBOX_ID", "SANDBOX_ID"},
		[]string{"/run/e2b/.E2B_SANDBOX_ID", "/run/e2b/E2B_SANDBOX_ID"},
	)
}

func templateID() string {
	return envOrFile(
		[]string{"E2B_TEMPLATE_ID", "SUPERCHARGE_E2B_TEMPLATE_ID", "SUPERCHARGE_E2B_TEMPLATE"},
		[]string{"/run/e2b/.E2B_TEMPLATE_ID", "/run/e2b/E2B_TEMPLATE_ID"},
	)
}

func previewDomain() string {
	domain := envOrFile([]string{"E2B_DOMAIN", "XIBECODE_E2B_PREVIEW_DOMAIN"}, nil)
	domain = strings.Trim(strings.TrimSpace(domain), ".")
	if domain == "" {
		return "e2b.app"
	}
	return domain
}

func previewURL(port int, sandbox, domain string) string {
	return fmt.Sprintf("https://%d-%s.%s", port, sandbox, domain)
}

// sandboxBrief is the identity the agent must use inside this VM.
func sandboxBrief() string {
	id := sandboxID()
	if id == "" {
		id = "unknown"
	}
	template := templateID()
	if template == "" {
		template = "unknown"
	}
	domain := previewDomain()
	var b strings.Builder
	b.WriteString("<sandbox>\n")
	b.WriteString("You are inside an E2B Linux VM, not on the person's computer.\n")
	fmt.Fprintf(&b, "Workspace: %s. List, read, write, and delete files only under that folder.\n", workspaceRoot())
	fmt.Fprintf(&b, "Sandbox id: %s\n", id)
	fmt.Fprintf(&b, "Template: %s\n", template)
	if id == "unknown" {
		b.WriteString("The sandbox id is missing from the environment. Read /run/e2b/.E2B_SANDBOX_ID and use that value. Do not invent an id.\n")
	} else {
		b.WriteString("If asked which sandbox or VM this is, answer with this sandbox id. Do not invent one.\n")
		fmt.Fprintf(&b, "A server on port N is reachable by the person at https://N-%s.%s\n", id, domain)
		b.WriteString("Never send localhost.\n")
		fmt.Fprintf(&b, "Examples: %s and %s\n", previewURL(3000, id, domain), previewURL(5173, id, domain))
	}
	b.WriteString("The desktop in this VM is controlled with desktop_observe and desktop_action. Observe before acting, then use that frame's coordinates.\n")
	b.WriteString("The person cannot open a disk path. Give a preview link, not /home/user/workspace.\n")
	b.WriteString("</sandbox>")
	return b.String()
}
