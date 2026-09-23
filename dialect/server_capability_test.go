package dialect

import (
	"context"
	"errors"
	"testing"
)

func TestVersionCapabilities(t *testing.T) {
	tests := []struct {
		name, dialect, version string
		window, forShare       bool
	}{
		{"mysql 5.7", MySQL, "5.7.44", false, false},
		{"mysql 5.7 log suffix", MySQL, "5.7.44-log", false, false},
		{"mysql 8.0.0 has windows but not FOR SHARE", MySQL, "8.0.0", true, false},
		{"mysql 8.0.1", MySQL, "8.0.1", true, true},
		{"mysql 8.4", MySQL, "8.4.3", true, true},
		{"mysql 10 (major compares numerically)", MySQL, "10.0.0", true, true},
		{"mariadb 10.1", MySQL, "10.1.48-MariaDB", false, false},
		{"mariadb 10.2", MySQL, "10.2.0-MariaDB-log", true, false},
		{"mariadb handshake prefix", MySQL, "5.5.5-10.6.12-MariaDB-1:10.6.12+maria~ubu2004", true, false},
		{"mariadb 11", MySQL, "11.4.2-MariaDB", true, false},
		{"unparseable mysql keeps the static set", MySQL, "garbage", false, true},
		{"postgres ignores version", Postgres, "9.6", true, true},
		{"sqlite ignores version", SQLite, "3.20.0", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := VersionCapabilities(tt.dialect, tt.version)
			if got := caps.Has(CapWindowFunctions); got != tt.window {
				t.Errorf("CapWindowFunctions = %v, want %v", got, tt.window)
			}
			if got := caps.Has(CapForShare); got != tt.forShare {
				t.Errorf("CapForShare = %v, want %v", got, tt.forShare)
			}
		})
	}
}

func TestGetCapabilities_WindowFunctionsStatic(t *testing.T) {
	if !GetCapabilities(Postgres).Has(CapWindowFunctions) {
		t.Error("postgres has window functions")
	}
	if !GetCapabilities(SQLite).Has(CapWindowFunctions) {
		t.Error("sqlite (3.25+) has window functions")
	}
	if GetCapabilities(MySQL).Has(CapWindowFunctions) {
		t.Error("the static MySQL set must not claim window functions: 5.7 lacks them, only a server probe may grant them")
	}
}

// capDriver is a Driver that reports a fixed server capability set.
type capDriver struct {
	Driver
	caps  Capabilities
	err   error
	calls int
}

func (d *capDriver) ServerCapabilities(context.Context) (Capabilities, error) {
	d.calls++
	return d.caps, d.err
}

type baseWrapper struct {
	Driver
	base Driver
}

func (w baseWrapper) BaseDriver() Driver { return w.base }

type nameDriver struct {
	Driver
	name string
}

func (d nameDriver) Dialect() string { return d.name }

func TestDriverCapabilities(t *testing.T) {
	ctx := context.Background()
	probed := Capabilities{CapWindowFunctions}

	t.Run("prober", func(t *testing.T) {
		d := &capDriver{caps: probed}
		got, err := DriverCapabilities(ctx, d)
		if err != nil || got != probed || d.calls != 1 {
			t.Fatalf("got %v, %v after %d calls", got, err, d.calls)
		}
	})
	t.Run("through debug and tx wrappers", func(t *testing.T) {
		d := &capDriver{caps: probed}
		wrapped := Debug(baseWrapper{base: d})
		got, err := DriverCapabilities(ctx, wrapped)
		if err != nil || got != probed {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("probe error", func(t *testing.T) {
		boom := errors.New("boom")
		if _, err := DriverCapabilities(ctx, &capDriver{err: boom}); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown driver falls back to the static set", func(t *testing.T) {
		got, err := DriverCapabilities(ctx, nameDriver{name: MySQL})
		if err != nil || got != GetCapabilities(MySQL) {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("nil", func(t *testing.T) {
		got, err := DriverCapabilities(ctx, baseWrapper{})
		if err != nil || got != (Capabilities{}) {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}
