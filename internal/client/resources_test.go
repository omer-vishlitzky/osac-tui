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
