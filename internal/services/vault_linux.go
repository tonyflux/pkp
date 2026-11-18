//go:build linux

package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/tonyflux/pkp/internal/i18n"
)

const (
	secretDestination      = "org.freedesktop.secrets"
	secretRoot             = dbus.ObjectPath("/org/freedesktop/secrets")
	secretServiceInterface = "org.freedesktop.Secret.Service"
	secretItemInterface    = "org.freedesktop.Secret.Item"
	secretPromptInterface  = "org.freedesktop.Secret.Prompt"
	secretTimeout          = 2 * time.Minute
)

// A plain Secret Service session means D-Bus transports the secret over the
// user's local session bus, not that the keyring stores plaintext on disk.
// The service owns unlocking dialogs and encrypted persistent storage.
type secretServiceVault struct{}

type secretService struct {
	conn    *dbus.Conn
	ctx     context.Context
	session dbus.ObjectPath
}

type serviceSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

func newSystemVault() PasswordVault { return secretServiceVault{} }
func keychainName() string          { return i18n.T("钥匙串") }

func withSecretService(fn func(*secretService) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), secretTimeout)
	defer cancel()
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if address == "" || address == "autolaunch:" {
		address = fmt.Sprintf("unix:path=/run/user/%d/bus", os.Getuid())
	}
	conn, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("Secret Service: session bus unavailable: %w", err)
	}
	defer conn.Close()
	s := &secretService{conn: conn, ctx: ctx}
	var output dbus.Variant
	if err := s.call(secretRoot, secretServiceInterface+".OpenSession", "plain", dbus.MakeVariant("")).Store(&output, &s.session); err != nil {
		return fmt.Errorf("Secret Service: open session: %w", err)
	}
	if !s.session.IsValid() || s.session == "/" {
		return errors.New("Secret Service: invalid session")
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		conn.Object(secretDestination, s.session).CallWithContext(closeCtx, "org.freedesktop.Secret.Session.Close", 0)
	}()
	if err := fn(s); err != nil {
		return fmt.Errorf("Secret Service: %w", err)
	}
	return nil
}

func (s *secretService) call(path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	return s.conn.Object(secretDestination, path).CallWithContext(s.ctx, method, 0, args...)
}

func secretAttributes(account string) map[string]string {
	return map[string]string{"application": "com.pkp.desktop", "account": account}
}

func (s *secretService) search(account string) ([]dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	if err := s.call(secretRoot, secretServiceInterface+".SearchItems", secretAttributes(account)).Store(&unlocked, &locked); err != nil {
		return nil, err
	}
	return append(unlocked, locked...), nil
}

// Subscribe before Prompt: some keyrings complete immediately, before its
// method reply. Match the service owner and object path, not arbitrary signals.
func (s *secretService) prompt(path dbus.ObjectPath) (dbus.Variant, error) {
	if path == "/" {
		return dbus.Variant{}, nil
	}
	if !path.IsValid() {
		return dbus.Variant{}, errors.New("invalid keyring prompt")
	}
	var owner string
	if err := s.conn.BusObject().CallWithContext(s.ctx, "org.freedesktop.DBus.GetNameOwner", 0, secretDestination).Store(&owner); err != nil {
		return dbus.Variant{}, err
	}
	options := []dbus.MatchOption{dbus.WithMatchSender(secretDestination), dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(secretPromptInterface), dbus.WithMatchMember("Completed")}
	signals := make(chan *dbus.Signal, 1)
	s.conn.Signal(signals)
	defer s.conn.RemoveSignal(signals)
	if err := s.conn.AddMatchSignalContext(s.ctx, options...); err != nil {
		return dbus.Variant{}, err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s.conn.RemoveMatchSignalContext(ctx, options...)
	}()
	if err := s.call(path, secretPromptInterface+".Prompt", "").Err; err != nil {
		return dbus.Variant{}, err
	}
	for {
		select {
		case signal := <-signals:
			if signal == nil || signal.Sender != owner || signal.Path != path || signal.Name != secretPromptInterface+".Completed" {
				continue
			}
			if len(signal.Body) != 2 {
				return dbus.Variant{}, errors.New("invalid keyring prompt response")
			}
			dismissed, ok := signal.Body[0].(bool)
			if !ok || dismissed {
				return dbus.Variant{}, errors.New("keyring prompt cancelled")
			}
			result, ok := signal.Body[1].(dbus.Variant)
			if !ok {
				return dbus.Variant{}, errors.New("invalid keyring prompt result")
			}
			return result, nil
		case <-s.ctx.Done():
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			s.conn.Object(secretDestination, path).CallWithContext(ctx, secretPromptInterface+".Dismiss", 0)
			return dbus.Variant{}, s.ctx.Err()
		}
	}
}

func (s *secretService) unlock(paths []dbus.ObjectPath) error {
	if len(paths) == 0 {
		return nil
	}
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := s.call(secretRoot, secretServiceInterface+".Unlock", paths).Store(&unlocked, &prompt); err != nil {
		return err
	}
	if prompt != "/" {
		result, err := s.prompt(prompt)
		if err != nil {
			return err
		}
		var prompted []dbus.ObjectPath
		if err := dbus.Store([]any{result.Value()}, &prompted); err != nil {
			return fmt.Errorf("invalid unlock result: %w", err)
		}
		unlocked = append(unlocked, prompted...)
	}
	for _, path := range paths {
		found := false
		for _, open := range unlocked {
			if path == open {
				found = true
				break
			}
		}
		if !found {
			return errors.New("keyring item remains locked")
		}
	}
	return nil
}

func (secretServiceVault) Password(account string) (string, error) {
	var password string
	err := withSecretService(func(s *secretService) error {
		items, err := s.search(account)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return ErrNoSavedPassword
		}
		if err := s.unlock(items[:1]); err != nil {
			return err
		}
		var secret serviceSecret
		if err := s.call(items[0], secretItemInterface+".GetSecret", s.session).Store(&secret); err != nil {
			return err
		}
		if secret.Session != s.session || len(secret.Parameters) != 0 {
			return errors.New("invalid plain-session secret")
		}
		password = string(secret.Value)
		return nil
	})
	return password, err
}

func (s *secretService) deleteItem(path dbus.ObjectPath) error {
	var prompt dbus.ObjectPath
	if err := s.call(path, secretItemInterface+".Delete").Store(&prompt); err != nil {
		return err
	}
	_, err := s.prompt(prompt)
	return err
}

func (secretServiceVault) SetPassword(account, password string) error {
	return withSecretService(func(s *secretService) error {
		items, err := s.search(account)
		if err != nil {
			return err
		}
		secret := serviceSecret{s.session, []byte{}, []byte(password), "text/plain; charset=utf-8"}
		if len(items) != 0 {
			if err := s.unlock(items); err != nil {
				return err
			}
			if err := s.call(items[0], secretItemInterface+".SetSecret", secret).Err; err != nil {
				return err
			}
			for _, item := range items[1:] {
				if err := s.deleteItem(item); err != nil {
					return err
				}
			}
			return nil
		}
		var collection dbus.ObjectPath
		if err := s.call(secretRoot, secretServiceInterface+".ReadAlias", "default").Store(&collection); err != nil {
			return err
		}
		if collection == "/" {
			var prompt dbus.ObjectPath
			properties := map[string]dbus.Variant{"org.freedesktop.Secret.Collection.Label": dbus.MakeVariant("PKP")}
			if err := s.call(secretRoot, secretServiceInterface+".CreateCollection", properties, "default").Store(&collection, &prompt); err != nil {
				return err
			}
			if prompt != "/" {
				result, err := s.prompt(prompt)
				if err != nil {
					return err
				}
				if err := dbus.Store([]any{result.Value()}, &collection); err != nil {
					return err
				}
			}
		}
		if !collection.IsValid() || collection == "/" {
			return errors.New("no default keyring collection")
		}
		if err := s.unlock([]dbus.ObjectPath{collection}); err != nil {
			return err
		}
		properties := map[string]dbus.Variant{
			secretItemInterface + ".Label":      dbus.MakeVariant("PKP — " + account),
			secretItemInterface + ".Attributes": dbus.MakeVariant(secretAttributes(account)),
		}
		var item, prompt dbus.ObjectPath
		if err := s.call(collection, "org.freedesktop.Secret.Collection.CreateItem", properties, secret, true).Store(&item, &prompt); err != nil {
			return err
		}
		if prompt != "/" {
			result, err := s.prompt(prompt)
			if err != nil {
				return err
			}
			if err := dbus.Store([]any{result.Value()}, &item); err != nil {
				return err
			}
		}
		if !item.IsValid() || item == "/" {
			return errors.New("keyring did not create an item")
		}
		return nil
	})
}

func (secretServiceVault) DeletePassword(account string) error {
	return withSecretService(func(s *secretService) error {
		items, err := s.search(account)
		if err != nil {
			return err
		}
		if err := s.unlock(items); err != nil {
			return err
		}
		for _, item := range items {
			if err := s.deleteItem(item); err != nil {
				return err
			}
		}
		return nil
	})
}
