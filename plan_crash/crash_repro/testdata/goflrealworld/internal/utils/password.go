package utils

import (
	"golang.org/x/crypto/bcrypt"
)

// passwordCost is the bcrypt work factor. goflrealworld: upstream used
// bcrypt.DefaultCost (10).
const passwordCost = 14 // BUG:WP8 every +1 doubles the work: 16x slower register and login than DefaultCost

// HashPassword hashes a password using bcrypt
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), passwordCost)
	return string(bytes), err
}

// CheckPassword compares a password with its hash
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
