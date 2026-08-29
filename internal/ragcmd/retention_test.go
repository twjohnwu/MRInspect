package ragcmd

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestRetention_KeepsLatestThreeVersions verifies REQ-09 / S-50: after a
// fourth package is published, retention keeps the three newest registry
// versions and turns a failed cleanup DELETE into a named warning.
func TestRetention_KeepsLatestThreeVersions(t *testing.T) {
	t.Run("deletes the oldest package by registry created_at", func(t *testing.T) {
		registry := newRetentionRegistry(
			fixturePackage(101, "1.0.0", 1),
			fixturePackage(102, "10.0.0", 2),
			fixturePackage(103, "9.0.0", 3),
		)
		published := fixturePackage(104, "2.0.0", 4)
		if err := registry.publish(published); err != nil {
			t.Fatalf("publish fourth version: %v", err)
		}

		result := runRetentionForTest(t, 3, registry)
		if result.Err != nil {
			t.Errorf("RunRetention error = %v, want successful retention", result.Err)
		}
		if !result.PublishSucceeded {
			t.Errorf("publish outcome = unsuccessful, want successful after retention")
		}
		if want := []int64{101}; !reflect.DeepEqual(registry.deleteCalls, want) {
			t.Errorf("DELETE package IDs = %v, want oldest package ID %v", registry.deleteCalls, want)
		}
		if want := []int64{104, 103, 102}; !reflect.DeepEqual(registry.remainingIDsByRecency(), want) {
			t.Errorf("remaining package IDs by created_at = %v, want newest three %v", registry.remainingIDsByRecency(), want)
		}
	})

	t.Run("failed delete is a named warning and does not change publish success", func(t *testing.T) {
		registry := newRetentionRegistry(
			fixturePackage(101, "1.0.0", 1),
			fixturePackage(102, "10.0.0", 2),
			fixturePackage(103, "9.0.0", 3),
		)
		if err := registry.publish(fixturePackage(104, "2.0.0", 4)); err != nil {
			t.Fatalf("publish fourth version: %v", err)
		}
		registry.deleteErr = errors.New("registry delete denied")

		result := runRetentionForTest(t, 3, registry)
		if result.Err != nil {
			t.Errorf("RunRetention error = %v, want success with warning", result.Err)
		}
		if !result.PublishSucceeded {
			t.Errorf("publish outcome = unsuccessful after failed cleanup, want successful")
		}
		if want := []int64{101}; !reflect.DeepEqual(registry.deleteCalls, want) {
			t.Errorf("failed DELETE package IDs = %v, want oldest package ID %v", registry.deleteCalls, want)
		}
		if !containsWarning(result.Warnings, "101") {
			t.Errorf("cleanup warnings = %q, want one naming failed DELETE package ID 101", result.Warnings)
		}
	})
}

// TestRetention_RejectsZeroKeepCount verifies REQ-09 / S-51: retention may
// keep one newest version, but zero and negative keep counts are configuration
// errors that issue no DELETE calls.
func TestRetention_RejectsZeroKeepCount(t *testing.T) {
	t.Run("keep one deletes the two oldest packages in created_at order", func(t *testing.T) {
		registry := newRetentionRegistry(
			fixturePackage(101, "10.0.0", 1),
			fixturePackage(102, "9.0.0", 2),
		)
		if err := registry.publish(fixturePackage(103, "2.0.0", 3)); err != nil {
			t.Fatalf("publish newest version: %v", err)
		}

		result := runRetentionForTest(t, 1, registry)
		if result.Err != nil {
			t.Errorf("RunRetention keep=1 error = %v, want successful retention", result.Err)
		}
		if want := []int64{101, 102}; !reflect.DeepEqual(registry.deleteCalls, want) {
			t.Errorf("DELETE package IDs = %v, want two oldest IDs in order %v", registry.deleteCalls, want)
		}
		if want := []int64{103}; !reflect.DeepEqual(registry.remainingIDsByRecency(), want) {
			t.Errorf("remaining package IDs by created_at = %v, want newest only %v", registry.remainingIDsByRecency(), want)
		}
	})

	for _, keep := range []int{0, -1} {
		t.Run(fmt.Sprintf("keep=%d is rejected before DELETE", keep), func(t *testing.T) {
			registry := newRetentionRegistry(
				fixturePackage(101, "10.0.0", 1),
				fixturePackage(102, "9.0.0", 2),
				fixturePackage(103, "2.0.0", 3),
			)

			result := runRetentionForTest(t, keep, registry)
			if result.Err == nil {
				t.Errorf("RunRetention keep=%d error = nil, want configuration error", keep)
			}
			if len(registry.deleteCalls) != 0 {
				t.Errorf("RunRetention keep=%d DELETE calls = %v, want none", keep, registry.deleteCalls)
			}
		})
	}
}

// retentionRegistry is a plain PackageRegistry test double. Its CreatedAt
// fields, not the lexical Version strings, are the fixture's recency source.
type retentionRegistry struct {
	versions    []PackageVersion
	deleteCalls []int64
	deleteErr   error
}

func newRetentionRegistry(versions ...PackageVersion) *retentionRegistry {
	return &retentionRegistry{versions: append([]PackageVersion(nil), versions...)}
}

func (r *retentionRegistry) ListPackageVersions(context.Context) ([]PackageVersion, error) {
	return append([]PackageVersion(nil), r.versions...), nil
}

func (r *retentionRegistry) DeletePackage(_ context.Context, packageID int64) error {
	r.deleteCalls = append(r.deleteCalls, packageID)
	if r.deleteErr != nil {
		return r.deleteErr
	}
	for i, version := range r.versions {
		if version.ID == packageID {
			r.versions = append(r.versions[:i], r.versions[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("package %d was not found", packageID)
}

func (r *retentionRegistry) publish(version PackageVersion) error {
	r.versions = append(r.versions, version)
	return nil
}

func (r *retentionRegistry) remainingIDsByRecency() []int64 {
	versions := append([]PackageVersion(nil), r.versions...)
	for i := range versions {
		for j := i + 1; j < len(versions); j++ {
			if versions[j].CreatedAt.After(versions[i].CreatedAt) {
				versions[i], versions[j] = versions[j], versions[i]
			}
		}
	}
	ids := make([]int64, len(versions))
	for i, version := range versions {
		ids[i] = version.ID
	}
	return ids
}

func fixturePackage(id int64, version string, day int) PackageVersion {
	return PackageVersion{ID: id, Version: version, CreatedAt: time.Date(2026, time.January, day, 0, 0, 0, 0, time.UTC)}
}

func runRetentionForTest(t *testing.T, keep int, registry PackageRegistry) (result RetentionResult) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Err = fmt.Errorf("RunRetention is still RED: %v", recovered)
		}
	}()
	return RunRetention(context.Background(), keep, registry)
}

func containsWarning(warnings []string, text string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, text) {
			return true
		}
	}
	return false
}
