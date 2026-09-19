package proxy

import (
	"os"
	"strings"
	"testing"
)

func TestAdminStylesDoNotBlockOnRemoteImports(t *testing.T) {
	styles, err := os.ReadFile("../web/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(styles)), "@import url(\"http") {
		t.Fatal("admin stylesheet must not block page rendering on a remote import")
	}
}
