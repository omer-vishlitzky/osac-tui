package client

import (
	"sort"
	"testing"
)

func TestResourcesIncludesAllPublicEntities(t *testing.T) {
	expectedWritable := []string{
		"baremetalinstancecatalogitems",
		"baremetalinstancetemplates",
		"baremetalinstances",
		"clustercatalogitems",
		"clustertemplates",
		"clusterversions",
		"clusters",
		"computeinstancecatalogitems",
		"computeinstancetemplates",
		"computeinstances",
		"diskimages",
		"externalipattachments",
		"externalips",
		"hosttypes",
		"identityproviders",
		"instancetypes",
		"natgateways",
		"projectmemberships",
		"projects",
		"rolebindings",
		"roles",
		"secrets",
		"securitygroups",
		"subnets",
		"tenants",
		"users",
		"virtualnetworks",
	}
	expectedReadOnly := []string{
		"addonoperators",
		"baremetalinstancetypes",
		"externalippools",
		"storagetiers",
		"volumes",
	}

	resources := resources(nil)
	if len(resources) != len(expectedWritable)+len(expectedReadOnly) {
		t.Fatalf("got %d resources, want %d", len(resources), len(expectedWritable)+len(expectedReadOnly))
	}

	for _, key := range expectedWritable {
		resource, ok := resources[key]
		if !ok {
			t.Errorf("missing writable resource %q", key)
			continue
		}
		if !resource.Writable() {
			t.Errorf("resource %q is unexpectedly read-only", key)
		}
	}
	for _, key := range expectedReadOnly {
		resource, ok := resources[key]
		if !ok {
			t.Errorf("missing read-only resource %q", key)
			continue
		}
		if resource.Writable() {
			t.Errorf("resource %q is unexpectedly writable", key)
		}
	}

	keys := resourceOrder(resources)
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("resource order is not sorted: %v", keys)
	}
}

func TestListFilterScopesTenant(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		options ListOptions
		want    string
	}{
		{
			name:    "resource tenant",
			key:     "projects",
			options: ListOptions{Tenant: "team-a"},
			want:    `this.metadata.tenant == "team-a"`,
		},
		{
			name:    "tenant resource",
			key:     "tenants",
			options: ListOptions{Tenant: "team-a"},
			want:    `this.metadata.name == "team-a"`,
		},
		{
			name:    "combines filters",
			key:     "projects",
			options: ListOptions{Tenant: "team-a", Filter: `this.metadata.name.startsWith("prod")`},
			want:    `(this.metadata.tenant == "team-a") && (this.metadata.name.startsWith("prod"))`,
		},
		{
			name:    "escapes tenant literal",
			key:     "projects",
			options: ListOptions{Tenant: `team" || true || "x`},
			want:    `this.metadata.tenant == "team\" || true || \"x"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := listFilter(test.key, test.options); got != test.want {
				t.Errorf("listFilter() = %q, want %q", got, test.want)
			}
		})
	}
}
