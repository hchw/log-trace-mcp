package sdk

import "testing"

func TestResolveSLSExplicitProject(t *testing.T) {
	p, l, err := ResolveSLS("myproject", "access-log")
	if err != nil {
		t.Fatal(err)
	}
	if p != "myproject" || l != "access-log" {
		t.Fatalf("got %q/%q", p, l)
	}
}

func TestResolveSLSTwoSegmentSource(t *testing.T) {
	p, l, err := ResolveSLS("", "myproject/access-log")
	if err != nil {
		t.Fatal(err)
	}
	if p != "myproject" || l != "access-log" {
		t.Fatalf("got %q/%q", p, l)
	}
}

func TestResolveSLSMissingProject(t *testing.T) {
	if _, _, err := ResolveSLS("", "access-log"); err == nil {
		t.Fatal("expected error when no project is given")
	}
}

func TestResolveSLSRejectsSlashWithProject(t *testing.T) {
	if _, _, err := ResolveSLS("proj", "proj/logstore"); err == nil {
		t.Fatal("expected error when source contains '/' and project is set")
	}
}
