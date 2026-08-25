package mountadapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"TDrive/backend/mountdav"
)

func TestEvaluateMutationConditions(t *testing.T) {
	current := conditionResource{exists: true, etag: mustETag(t, "f:41", 3, "abc")}
	missing := conditionResource{}

	tests := []struct {
		name       string
		conditions mountdav.MutationConditions
		request    conditionResource
		resources  map[string]conditionResource
		wantErr    error
	}{
		{name: "none", request: current},
		{
			name: "strong if match",
			conditions: mountdav.MutationConditions{IfMatch: mountdav.ETagConditions{
				Present: true,
				Tags:    []mountdav.EntityTag{{Opaque: opaqueETag(current.etag)}},
			}},
			request: current,
		},
		{
			name: "weak if match never matches",
			conditions: mountdav.MutationConditions{IfMatch: mountdav.ETagConditions{
				Present: true,
				Tags:    []mountdav.EntityTag{{Weak: true, Opaque: opaqueETag(current.etag)}},
			}},
			request: current,
			wantErr: mountdav.ErrWritePreconditionFailed,
		},
		{
			name:       "if match star needs an object",
			conditions: mountdav.MutationConditions{IfMatch: mountdav.ETagConditions{Present: true, Any: true}},
			request:    missing,
			wantErr:    mountdav.ErrWritePreconditionFailed,
		},
		{
			name: "weak if none match compares weakly",
			conditions: mountdav.MutationConditions{IfNoneMatch: mountdav.ETagConditions{
				Present: true,
				Tags:    []mountdav.EntityTag{{Weak: true, Opaque: opaqueETag(current.etag)}},
			}},
			request: current,
			wantErr: mountdav.ErrWritePreconditionFailed,
		},
		{
			name:       "if none match star allows create",
			conditions: mountdav.MutationConditions{IfNoneMatch: mountdav.ETagConditions{Present: true, Any: true}},
			request:    missing,
		},
		{
			name: "dav lists are or within a resource",
			conditions: mountdav.MutationConditions{DAVIf: []mountdav.DAVConditionList{
				{ResourcePath: "/source", Conditions: []mountdav.DAVCondition{{ETag: &mountdav.EntityTag{Opaque: "wrong"}}}},
				{ResourcePath: "/source", Conditions: []mountdav.DAVCondition{{ETag: &mountdav.EntityTag{Opaque: opaqueETag(current.etag)}}}},
			}},
			request:   current,
			resources: map[string]conditionResource{"/source": current},
		},
		{
			name: "dav resource groups are anded",
			conditions: mountdav.MutationConditions{DAVIf: []mountdav.DAVConditionList{
				{ResourcePath: "/source", Conditions: []mountdav.DAVCondition{{ETag: &mountdav.EntityTag{Opaque: opaqueETag(current.etag)}}}},
				{ResourcePath: "/destination", Conditions: []mountdav.DAVCondition{{ETag: &mountdav.EntityTag{Opaque: "wrong"}}}},
			}},
			request: current,
			resources: map[string]conditionResource{
				"/source":      current,
				"/destination": current,
			},
			wantErr: mountdav.ErrWritePreconditionFailed,
		},
		{
			name: "validated lock token",
			conditions: mountdav.MutationConditions{
				DAVIf:      []mountdav.DAVConditionList{{ResourcePath: "/source", Conditions: []mountdav.DAVCondition{{LockToken: "urn:lock:1"}}}},
				LockTokens: []string{"urn:lock:1"},
			},
			request:   current,
			resources: map[string]conditionResource{"/source": current},
		},
		{
			name: "arbitrary tagged resource is rejected",
			conditions: mountdav.MutationConditions{DAVIf: []mountdav.DAVConditionList{{
				ResourcePath: "/unrelated",
				Conditions:   []mountdav.DAVCondition{{ETag: &mountdav.EntityTag{Opaque: opaqueETag(current.etag)}}},
			}}},
			request:   current,
			resources: map[string]conditionResource{"/source": current},
			wantErr:   mountdav.ErrWritePreconditionFailed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := evaluateMutationConditions(test.conditions, test.request, test.resources)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("evaluateMutationConditions() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestStrongETagIsOpaqueAndStable(t *testing.T) {
	first := mustETag(t, "f:41", 7, "private-content-hash")
	second := mustETag(t, "f:41", 7, "private-content-hash")
	if first != second {
		t.Fatalf("ETag is not stable: %q != %q", first, second)
	}
	if first[0] != '"' || first[len(first)-1] != '"' {
		t.Fatalf("ETag %q is not strongly quoted", first)
	}
	for _, secret := range []string{"f:41", "private-content-hash"} {
		if strings.Contains(first, secret) {
			t.Fatalf("ETag %q leaked %q", first, secret)
		}
	}
}

func mustETag(t *testing.T, objectID string, revision int64, contentHash string) string {
	t.Helper()
	value, err := mountdav.ResourceETag(context.Background(), testDriveID, objectID, revision, contentHash)
	if err != nil {
		t.Fatalf("ResourceETag: %v", err)
	}
	return value
}
