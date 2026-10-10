package api

import (
	"errors"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	phoneRe = regexp.MustCompile(`^[0-9+\-() ]+$`)
	codeRe  = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]*$`)
	digitRe = regexp.MustCompile(`[0-9]`)
)

// validName trims and bounds a required business name.
func validName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxNameLen {
		return "", false
	}
	return s, true
}

// validPhone trims and validates an optional contact number. Phone is optional
// per the documented business rule; when present it must use only plausible
// phone characters (digits, spaces, +, -, parentheses) and carry 6-20 digits so
// international prefixes are supported without assuming a fixed 10-digit format.
func validPhone(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true
	}
	if len(s) > maxPhoneLen || !phoneRe.MatchString(s) {
		return "", false
	}
	digits := len(digitRe.FindAllString(s, -1))
	if digits < 6 || digits > 20 {
		return "", false
	}
	return s, true
}

// normalizeCode defines the canonical SKU-code form: trimmed and upper-cased.
func normalizeCode(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// validCode normalizes and validates a required, unique SKU code.
func validCode(s string) (string, bool) {
	s = normalizeCode(s)
	if s == "" || len(s) > maxCodeLen || !codeRe.MatchString(s) {
		return "", false
	}
	return s, true
}

// validImageURL validates an optional image URL. Empty is allowed. When present
// it must be a syntactically valid absolute http/https URL; the server never
// fetches it (SSRF avoidance) — the browser renders it with a fallback.
func validImageURL(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true
	}
	if len(s) > maxURLLen {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return s, true
}

var errRupees = errors.New("rate must be rupees with at most 2 decimal places and within range")

// parseRupeesToPaise converts a rupee amount given as a decimal string to an
// exact integer paise value using only integer arithmetic (no floating point).
// It rejects more than two decimal places, non-numeric input, and overflow.
func parseRupeesToPaise(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errRupees
	}
	whole, frac := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		whole, frac = s[:dot], s[dot+1:]
	}
	if whole == "" || len(frac) > 2 {
		return 0, errRupees
	}
	rupees, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || rupees < 0 {
		return 0, errRupees
	}
	var paiseFrac int64
	if frac != "" {
		padded := frac
		if len(padded) == 1 {
			padded += "0"
		}
		paiseFrac, err = strconv.ParseInt(padded, 10, 64)
		if err != nil || paiseFrac < 0 {
			return 0, errRupees
		}
	}
	if rupees > (math.MaxInt64-paiseFrac)/100 {
		return 0, errRupees
	}
	return rupees*100 + paiseFrac, nil
}
