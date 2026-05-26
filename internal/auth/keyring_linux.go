//go:build linux

package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

// Linux Chromium derives the cookie AES key with a single PBKDF2 iteration
// (macOS uses 1003). See cookies.go's aesDecrypt.
const linuxIterations = 1

const (
	secretServiceName = "org.freedesktop.secrets"
	secretServicePath = "/org/freedesktop/secrets"
	// secretSafeLabel is the label Chromium/Electron gives the keyring item
	// holding the per-app cookie key: "<product> Safe Storage".
	secretSafeLabel = "Slack Safe Storage"
)

// cookieKey resolves a Linux cookie version prefix to the (password,
// iterations) pair aesDecrypt needs:
//   - v10: Chromium's hardcoded "basic" password store password ("peanuts")
//   - v11: the per-app key stored in the Secret Service (GNOME Keyring/KWallet)
func cookieKey(_ *Source, prefix string) ([]byte, int, error) {
	switch prefix {
	case "v10":
		return []byte("peanuts"), linuxIterations, nil
	case "v11":
		key, err := secretServiceKey()
		if err != nil {
			return nil, 0, err
		}
		return key, linuxIterations, nil
	default:
		return nil, 0, fmt.Errorf("unsupported cookie prefix %q on linux", prefix)
	}
}

// secret mirrors the Secret Service "Secret" struct (oayays): session path,
// parameters, value, content type.
type secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// secretServiceKey reads the "Slack Safe Storage" key from the freedesktop
// Secret Service over D-Bus (GNOME Keyring, KWallet, ...), unlocking the
// keyring first if necessary.
func secretServiceKey() ([]byte, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect to D-Bus session bus: %w (is a desktop session running?)", err)
	}

	service := conn.Object(secretServiceName, secretServicePath)

	var ignored dbus.Variant
	var session dbus.ObjectPath
	if err := service.Call("org.freedesktop.Secret.Service.OpenSession", 0, "plain", dbus.MakeVariant("")).
		Store(&ignored, &session); err != nil {
		return nil, fmt.Errorf("open Secret Service session: %w", err)
	}

	item, err := findSlackItem(conn, service)
	if err != nil {
		return nil, err
	}

	if err := unlockItem(conn, service, item); err != nil {
		return nil, err
	}

	var sec secret
	if err := conn.Object(secretServiceName, item).
		Call("org.freedesktop.Secret.Item.GetSecret", 0, session).Store(&sec); err != nil {
		return nil, fmt.Errorf("read Slack Safe Storage secret: %w", err)
	}
	if len(sec.Value) == 0 {
		return nil, errors.New("keyring returned an empty Slack Safe Storage key")
	}
	return sec.Value, nil
}

// findSlackItem locates the keyring item holding Slack's cookie key. The exact
// attribute schema varies, so it tries attribute searches first, then falls
// back to scanning every item for the canonical label.
func findSlackItem(conn *dbus.Conn, service dbus.BusObject) (dbus.ObjectPath, error) {
	for _, attrs := range []map[string]string{
		{"application": "Slack"},
		{"application": "slack"},
	} {
		if p, ok := searchOne(service, attrs); ok {
			return p, nil
		}
	}

	unlocked, locked := searchItems(service, map[string]string{})
	all := append(append([]dbus.ObjectPath{}, unlocked...), locked...)
	for _, p := range all {
		if itemLabel(conn, p) == secretSafeLabel {
			return p, nil
		}
	}
	return "", errors.New(`no "Slack Safe Storage" key found in the Secret Service keyring; is Slack signed in and your login keyring unlocked?`)
}

func searchItems(service dbus.BusObject, attrs map[string]string) (unlocked, locked []dbus.ObjectPath) {
	_ = service.Call("org.freedesktop.Secret.Service.SearchItems", 0, attrs).Store(&unlocked, &locked)
	return unlocked, locked
}

func searchOne(service dbus.BusObject, attrs map[string]string) (dbus.ObjectPath, bool) {
	unlocked, locked := searchItems(service, attrs)
	if len(unlocked) > 0 {
		return unlocked[0], true
	}
	if len(locked) > 0 {
		return locked[0], true
	}
	return "", false
}

func itemLabel(conn *dbus.Conn, item dbus.ObjectPath) string {
	v, err := conn.Object(secretServiceName, item).GetProperty("org.freedesktop.Secret.Item.Label")
	if err != nil {
		return ""
	}
	s, _ := v.Value().(string)
	return s
}

// unlockItem unlocks the item (and so its collection) if it is locked. A
// locked keyring returns a prompt object; we trigger it and wait for the
// user to authenticate.
func unlockItem(conn *dbus.Conn, service dbus.BusObject, item dbus.ObjectPath) error {
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := service.Call("org.freedesktop.Secret.Service.Unlock", 0, []dbus.ObjectPath{item}).
		Store(&unlocked, &prompt); err != nil {
		return fmt.Errorf("unlock keyring item: %w", err)
	}
	if prompt == "/" || prompt == "" {
		return nil // already unlocked, no prompt needed
	}
	return performPrompt(conn, prompt)
}

func performPrompt(conn *dbus.Conn, prompt dbus.ObjectPath) error {
	signals := make(chan *dbus.Signal, 1)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)

	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(prompt),
		dbus.WithMatchInterface("org.freedesktop.Secret.Prompt"),
		dbus.WithMatchMember("Completed"),
	); err != nil {
		return fmt.Errorf("subscribe to keyring prompt: %w", err)
	}

	if call := conn.Object(secretServiceName, prompt).
		Call("org.freedesktop.Secret.Prompt.Prompt", 0, ""); call.Err != nil {
		return fmt.Errorf("trigger keyring unlock prompt: %w", call.Err)
	}

	deadline := time.After(2 * time.Minute)
	for {
		select {
		case sig := <-signals:
			if sig.Path != prompt || sig.Name != "org.freedesktop.Secret.Prompt.Completed" {
				continue
			}
			if len(sig.Body) >= 1 {
				if dismissed, _ := sig.Body[0].(bool); dismissed {
					return errors.New("keyring unlock prompt was dismissed")
				}
			}
			return nil
		case <-deadline:
			return errors.New("timed out waiting for keyring to be unlocked")
		}
	}
}
