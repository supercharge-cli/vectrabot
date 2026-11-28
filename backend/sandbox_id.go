package main

import (
	"fmt"
	"os"
	"regexp"
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
func sandboxBrief(botID string) string {
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
		b.WriteString("When you share a server, write that full link. Never send localhost, 127.0.0.1, or a bare port for the person to type into the preview.\n")
		fmt.Fprintf(&b, "Examples: %s and %s\n", previewURL(3000, id, domain), previewURL(5173, id, domain))
	}
	b.WriteString("You have your own desktop, separate from the other bots. Control it with desktop_observe and desktop_action. Do not use another bot's display.\n")
	b.WriteString("Those calls are for you. Do not put a screenshot in the chat unless the person asked to see the screen, or you are showing a result they asked for.\n")
	b.WriteString("Observe, act on that frame only, then observe again. Do one small step at a time.\n")
	b.WriteString("Click the window you will type into before typing.\n")
	if desk := ensureBotDesktop(botID); desk.display != "" {
		fmt.Fprintf(&b, "desktop_action type often inserts nothing, or only ^M. After the click, type with that display instead: DISPLAY=%s xdotool type --delay 12 -- 'the text' && DISPLAY=%s xdotool key Return.\n", desk.display, desk.display)
	}
	b.WriteString("Use run_terminal_command for shell work that does not need the picture. Observe again after either one.\n")
	b.WriteString("If the screen did not change, click that window again and retry once.\n")
	b.WriteString("Do not launch a guessed application when that window is already open.\n")
	b.WriteString("The person cannot open a disk path. Give a preview link, not /home/user/workspace.\n")
	b.WriteString("Sites refuse agents for four different reasons. Work out which one you hit before you react, because the fix for each is different and the wrong one wastes the attempt.\n")
	b.WriteString("1. A puzzle. A shape, word, sum or image grid. That is ordinary work, not a secret, so solve it before you report anything: read it from a screenshot, work out the answer, enter it with browser_fill (or desktop_action for a canvas), then confirm with a fresh screenshot. Image grids take one click per matching tile, then the verify button. Only report a puzzle when two tries have failed.\n")
	b.WriteString("2. A banned address. A block page that stays after one reload, often worded unusual traffic, denied or forbidden. Solving the puzzle on it does not lift it. Say so plainly and stop.\n")
	b.WriteString("3. A behaviour flag. The page or firewall refused after your own actions, for example a login that always fails at the same step. Change how you act: click with desktop_action so the page sees a real pointer, instead of setting focus or clicking from script, and slow down.\n")
	b.WriteString("4. A region wall. The site serves one country and refuses this machine. Say so plainly and stop.\n")
	b.WriteString("Never loop. Two tries on the same wall is the limit, then report which kind it is and what would fix it. Never use a CAPTCHA-solving service, never disguise the browser fingerprint, and never retry a puzzle that already rejected a correct-looking answer.\n")
	b.WriteString("Only a challenge that is scored rather than answered, such as reCAPTCHA v3 or Cloudflare Turnstile, stays with the person: there is nothing to solve there, so explain that plainly and call browser_handoff with action start.\n")
	b.WriteString("Passwords, MFA codes, passkeys, OAuth consent, and payment details are still the person's. Call browser_handoff for those and never ask them to paste a secret into the chat.\n")
	b.WriteString("</sandbox>")
	return b.String()
}

var localServerLink = regexp.MustCompile(`(?i)(?:https?://)?(?:localhost|127\.0\.0\.1|0\.0\.0\.0):(\d{2,5})(/[^\s)\]>'"]*)?`)

// publicizeLinks rewrites a loopback server address into the sandbox's public URL.
func publicizeLinks(text string) string {
	id := sandboxID()
	if id == "" {
		return text
	}
	domain := previewDomain()
	return localServerLink.ReplaceAllStringFunc(text, func(match string) string {
		parts := localServerLink.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		port := 0
		fmt.Sscanf(parts[1], "%d", &port)
		if port < 1 || port > 65535 {
			return match
		}
		path := ""
		if len(parts) > 2 {
			path = parts[2]
		}
		return previewURL(port, id, domain) + path
	})
}
