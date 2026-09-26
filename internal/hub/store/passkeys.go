package store

import (
	"crypto/rand"
	"errors"
	"strings"
	"time"
)

// Passkey is a WebAuthn credential that signs in one user without a password.
type Passkey struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"-"`
	Name         string `json:"name"`
	RPID         string `json:"rp_id"`
	CredentialID []byte `json:"-"`
	PublicKey    []byte `json:"-"` // SubjectPublicKeyInfo, DER
	Algorithm    int64  `json:"-"`
	SignCount    uint32 `json:"-"`
	CreatedAt    int64  `json:"created_at"`
	LastUsed     int64  `json:"last_used"`
}

// ErrPasskeyExists is returned when a credential is registered twice.
var ErrPasskeyExists = errors.New("this passkey is already registered")

const passkeyCols = "id, user_id, name, rp_id, credential_id, public_key, algorithm, sign_count, created_at, last_used"

func scanPasskey(row interface{ Scan(...any) error }) (*Passkey, error) {
	p := &Passkey{}
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.RPID, &p.CredentialID, &p.PublicKey, &p.Algorithm, &p.SignCount,
		&p.CreatedAt, &p.LastUsed)
	return p, notFound(err)
}

// PasskeyHandle returns the user's WebAuthn user handle, creating it on first use.
// It is random so it reveals nothing about the account.
func (s *Store) PasskeyHandle(userID int64) ([]byte, error) {
	var handle []byte
	err := s.db.QueryRow("SELECT passkey_handle FROM users WHERE id = ?", userID).Scan(&handle)
	if err != nil || len(handle) > 0 {
		return handle, notFound(err)
	}
	handle = make([]byte, 16)
	rand.Read(handle)
	if _, err := s.db.Exec("UPDATE users SET passkey_handle = ? WHERE id = ? AND passkey_handle IS NULL", handle, userID); err != nil {
		return nil, err
	}
	// Another request may have set it first; read back whichever won.
	err = s.db.QueryRow("SELECT passkey_handle FROM users WHERE id = ?", userID).Scan(&handle)
	return handle, notFound(err)
}

func (s *Store) CreatePasskey(p *Passkey) error {
	p.CreatedAt = time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO passkeys (user_id, name, rp_id, credential_id, public_key, algorithm, sign_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, p.UserID, p.Name, p.RPID, p.CredentialID, p.PublicKey, p.Algorithm, p.SignCount, p.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrPasskeyExists
		}
		return err
	}
	p.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) Passkeys(userID int64) ([]*Passkey, error) {
	rows, err := s.db.Query("SELECT "+passkeyCols+" FROM passkeys WHERE user_id = ? ORDER BY id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Passkey{}
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PasskeyCounts returns the number of passkeys per user ID.
func (s *Store) PasskeyCounts() (map[int64]int, error) {
	rows, err := s.db.Query("SELECT user_id, COUNT(*) FROM passkeys GROUP BY user_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (s *Store) PasskeyByCredential(credentialID []byte) (*Passkey, error) {
	return scanPasskey(s.db.QueryRow("SELECT "+passkeyCols+" FROM passkeys WHERE credential_id = ?", credentialID))
}

// UsePasskey records a successful sign-in and the authenticator's new counter.
func (s *Store) UsePasskey(id int64, signCount uint32) error {
	_, err := s.db.Exec("UPDATE passkeys SET sign_count = ?, last_used = ? WHERE id = ?", signCount, time.Now().Unix(), id)
	return err
}

func (s *Store) RenamePasskey(id, userID int64, name string) error {
	res, err := s.db.Exec("UPDATE passkeys SET name = ? WHERE id = ? AND user_id = ?", name, id, userID)
	return affected(res, err)
}

// DeletePasskey removes one of the user's passkeys.
func (s *Store) DeletePasskey(id, userID int64) (*Passkey, error) {
	p, err := scanPasskey(s.db.QueryRow("SELECT "+passkeyCols+" FROM passkeys WHERE id = ? AND user_id = ?", id, userID))
	if err != nil {
		return nil, err
	}
	res, err := s.db.Exec("DELETE FROM passkeys WHERE id = ?", id)
	return p, affected(res, err)
}

// DeletePasskeysOf removes all passkeys of a user and returns how many there were.
func (s *Store) DeletePasskeysOf(userID int64) (int64, error) {
	res, err := s.db.Exec("DELETE FROM passkeys WHERE user_id = ?", userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UserByPasskeyHandle finds the account a discoverable credential belongs to.
func (s *Store) UserByPasskeyHandle(handle []byte) (*User, error) {
	if len(handle) == 0 {
		return nil, ErrNotFound
	}
	return scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE passkey_handle = ?", handle))
}
