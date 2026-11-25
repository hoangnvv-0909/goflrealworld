package report

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func hashIdentifiers(doc *document) error {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return fmt.Errorf("generate report hash key: %w", err)
	}
	token := func(kind, value string) string {
		if value == "" {
			return ""
		}
		mac := hmac.New(sha256.New, key[:])
		mac.Write([]byte(kind + "\x00" + value))
		return kind + "_" + hex.EncodeToString(mac.Sum(nil))
	}

	doc.Run.BaseProfile = ""
	doc.Run.ScaleProfile = ""
	for i := range doc.Hotspots {
		item := &doc.Hotspots[i]
		item.Function = token("fn", item.Function)
		item.File = token("file", item.File)
	}
	for i := range doc.CriticalPaths {
		path := &doc.CriticalPaths[i]
		path.Target = token("fn", path.Target)
		signature := make([]string, 0, len(path.Frames))
		for j := range path.Frames {
			frame := &path.Frames[j]
			frame.Name = token("fn", frame.Name)
			frame.File = token("file", frame.File)
			signature = append(signature, frame.File+"("+frame.Name+")")
		}
		if len(signature) == 0 {
			path.Signature = token("path", path.Signature)
		} else {
			path.Signature = strings.Join(signature, " -> ")
		}
	}
	return nil
}
