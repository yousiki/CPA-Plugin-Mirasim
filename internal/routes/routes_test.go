package routes

import "testing"

func TestResourceSuffix(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: ResourcePrefix + Status, want: Status},
		{path: ResourcePrefix + StatusData, want: StatusData},
		// The host may or may not keep a trailing slash.
		{path: ResourcePrefix + Status + "/", want: Status},
		{path: ResourcePrefix + Login, want: Login},
		// Already-stripped paths route the same way.
		{path: Status, want: Status},
		{path: "/something/else", want: "/something/else"},
	}

	for _, test := range tests {
		if got := ResourceSuffix(test.path); got != test.want {
			t.Errorf("ResourceSuffix(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

func TestIsManagement(t *testing.T) {
	if !IsManagement(ManagementPrefix+Quota, Quota) {
		t.Errorf("IsManagement(%q) = false, want true", ManagementPrefix+Quota)
	}
	if !IsManagement(ManagementPrefix+Quota+"/", Quota) {
		t.Error("a trailing slash should still match")
	}
	// The console's own routes must never be mistaken for the management-authenticated
	// quota feed: that one is served without a console token.
	if IsManagement(ResourcePrefix+StatusData, Quota) {
		t.Error("a resource path matched the management quota route")
	}
}
