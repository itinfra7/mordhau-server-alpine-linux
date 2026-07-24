package manager

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,31}$`)

func validateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("username must be 3-32 characters using lowercase letters, digits, _ or -")
	}
	return nil
}

func validateWebPassword(password string) error {
	length := utf8.RuneCountInString(password)
	if length < 8 || length > 128 {
		return errors.New("password must be 8-128 characters")
	}
	var lower, upper, digit bool
	for _, character := range password {
		lower = lower || unicode.IsLower(character)
		upper = upper || unicode.IsUpper(character)
		digit = digit || unicode.IsDigit(character)
	}
	if !lower || !upper || !digit {
		return errors.New("password must contain a lowercase letter, uppercase letter, and digit")
	}
	return nil
}

func (m *Manager) authenticate(username, password string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, account := range m.accounts.Accounts {
		if account.Username == username {
			return verifyPassword(account, password)
		}
	}
	// Keep unknown-user and wrong-password requests similarly expensive.
	dummy := Account{
		Salt: "MDEyMzQ1Njc4OWFiY2RlZg",
		Hash: "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA",
	}
	_ = verifyPassword(dummy, password)
	return false
}

func (m *Manager) createSession(username string, remember bool) (string, Session, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", Session{}, err
	}
	csrf, err := randomToken(24)
	if err != nil {
		return "", Session{}, err
	}
	duration := 24 * time.Hour
	if remember {
		duration = 30 * 24 * time.Hour
	}
	now := time.Now()
	session := Session{
		TokenHash: hashToken(token),
		CSRF:      csrf,
		Username:  username,
		ExpiresAt: now.Add(duration),
		Remember:  remember,
		CreatedAt: now,
	}
	m.mu.Lock()
	m.purgeSessionsLocked(now)
	m.sessions.Sessions = append(m.sessions.Sessions, session)
	err = writeJSONAtomic(sessionsPath, m.sessions, 0600)
	m.mu.Unlock()
	if err != nil {
		return "", Session{}, err
	}
	return token, session, nil
}

func (m *Manager) sessionForRequest(request *http.Request) (Session, bool) {
	cookie, err := request.Cookie("mordhau_session")
	if err != nil || cookie.Value == "" {
		return Session{}, false
	}
	tokenHash := hashToken(cookie.Value)
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, session := range m.sessions.Sessions {
		if session.TokenHash == tokenHash && now.Before(session.ExpiresAt) {
			return session, true
		}
	}
	return Session{}, false
}

func (m *Manager) deleteSession(token string) {
	if token == "" {
		return
	}
	tokenHash := hashToken(token)
	m.mu.Lock()
	kept := m.sessions.Sessions[:0]
	for _, session := range m.sessions.Sessions {
		if session.TokenHash != tokenHash {
			kept = append(kept, session)
		}
	}
	m.sessions.Sessions = kept
	_ = writeJSONAtomic(sessionsPath, m.sessions, 0600)
	m.mu.Unlock()
}

func (m *Manager) invalidateAccountSessionsLocked(username string) {
	kept := m.sessions.Sessions[:0]
	for _, session := range m.sessions.Sessions {
		if session.Username != username {
			kept = append(kept, session)
		}
	}
	m.sessions.Sessions = kept
}

func (m *Manager) createAccount(username, password string) error {
	username = strings.TrimSpace(username)
	if err := validateUsername(username); err != nil {
		return err
	}
	if err := validateWebPassword(password); err != nil {
		return err
	}
	salt, hash, err := makePasswordHash(password)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, account := range m.accounts.Accounts {
		if account.Username == username {
			return errors.New("username already exists")
		}
	}
	now := time.Now()
	m.accounts.Accounts = append(m.accounts.Accounts, Account{
		Username:  username,
		Salt:      salt,
		Hash:      hash,
		CreatedAt: now,
		UpdatedAt: now,
	})
	return writeJSONAtomic(accountsPath, m.accounts, 0600)
}

func (m *Manager) editAccount(oldUsername, newUsername, password string) error {
	oldUsername = strings.TrimSpace(oldUsername)
	newUsername = strings.TrimSpace(newUsername)
	if err := validateUsername(newUsername); err != nil {
		return err
	}
	if password != "" {
		if err := validateWebPassword(password); err != nil {
			return err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	index := -1
	for i, account := range m.accounts.Accounts {
		if account.Username == oldUsername {
			index = i
		}
		if account.Username == newUsername && oldUsername != newUsername {
			return errors.New("username already exists")
		}
	}
	if index < 0 {
		return errors.New("account not found")
	}
	account := m.accounts.Accounts[index]
	account.Username = newUsername
	account.UpdatedAt = time.Now()
	if password != "" {
		salt, hash, err := makePasswordHash(password)
		if err != nil {
			return err
		}
		account.Salt = salt
		account.Hash = hash
	}
	m.accounts.Accounts[index] = account
	m.invalidateAccountSessionsLocked(oldUsername)
	if err := writeJSONAtomic(accountsPath, m.accounts, 0600); err != nil {
		return err
	}
	return writeJSONAtomic(sessionsPath, m.sessions, 0600)
}

func (m *Manager) deleteAccount(username string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.accounts.Accounts) < 2 {
		return errors.New("the last account cannot be deleted")
	}
	index := -1
	for i, account := range m.accounts.Accounts {
		if account.Username == username {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("account not found")
	}
	m.accounts.Accounts = append(m.accounts.Accounts[:index], m.accounts.Accounts[index+1:]...)
	m.invalidateAccountSessionsLocked(username)
	if err := writeJSONAtomic(accountsPath, m.accounts, 0600); err != nil {
		return err
	}
	return writeJSONAtomic(sessionsPath, m.sessions, 0600)
}

func (m *Manager) loginPermitted(ip string, now time.Time) bool {
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	attempt := m.loginAttempts[ip]
	if attempt == nil {
		return true
	}
	return !now.Before(attempt.BlockedTo)
}

func (m *Manager) recordLoginFailure(ip string, now time.Time) {
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	attempt := m.loginAttempts[ip]
	if attempt == nil {
		attempt = &loginAttempt{}
		m.loginAttempts[ip] = attempt
	}
	cutoff := now.Add(-5 * time.Minute)
	kept := attempt.Failures[:0]
	for _, failure := range attempt.Failures {
		if failure.After(cutoff) {
			kept = append(kept, failure)
		}
	}
	attempt.Failures = append(kept, now)
	if len(attempt.Failures) >= 5 {
		attempt.BlockedTo = now.Add(15 * time.Minute)
	}
}

func (m *Manager) clearLoginFailures(ip string) {
	m.loginMu.Lock()
	delete(m.loginAttempts, ip)
	m.loginMu.Unlock()
}

func (m *Manager) pruneLoginAttempts(now time.Time) {
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	for ip, attempt := range m.loginAttempts {
		latest := time.Time{}
		for _, failure := range attempt.Failures {
			if failure.After(latest) {
				latest = failure
			}
		}
		if now.After(attempt.BlockedTo) && now.Sub(latest) > 30*time.Minute {
			delete(m.loginAttempts, ip)
		}
	}
}
