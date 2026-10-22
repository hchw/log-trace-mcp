package sdk

import (
	"reflect"
	"testing"
)

func TestLanguagesForTLS(t *testing.T) {
	got := LanguagesFor(EngineTLS)
	want := []string{"sql", "search"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LanguagesFor(tls) = %v, want %v", got, want)
	}
}

func TestValidLanguageTLS(t *testing.T) {
	if !ValidLanguage(EngineTLS, "sql") || !ValidLanguage(EngineTLS, "search") {
		t.Fatal("expected sql and search to be valid for TLS")
	}
	for _, lang := range []string{"spl", "dsl", "ppl", "nope"} {
		if ValidLanguage(EngineTLS, lang) {
			t.Fatalf("%q must not be valid for TLS", lang)
		}
	}
}
