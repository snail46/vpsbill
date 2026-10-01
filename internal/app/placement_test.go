package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"vpsbill/internal/store/postgres"
)

func TestValidatePlacement(t *testing.T) {
	retired := time.Now()
	node := func(id, providerType string, virtualization ...string) postgres.Node {
		return postgres.Node{ID: id, Name: id, NodeEndpoint: postgres.NodeEndpoint{ProviderType: providerType}, VirtualizationTypes: virtualization}
	}
	nodes := []postgres.Node{
		node("a", "hatch", "lxc", "podman"),
		node("b", "hatch", "lxc"),
		node("c", "clicd", "lxc"),
	}
	hosted := node("h", "hatch", "lxc")
	hosted.OwnerAccountID = "owner"
	gone := node("r", "hatch", "lxc")
	gone.RetiredAt = &retired
	nodes = append(nodes, hosted, gone)

	cases := []struct {
		selection string
		ids       []string
		virt      string
		want      string
		wantIDs   []string
	}{
		{"", []string{"a"}, "lxc", "", []string{}},
		{"spread", []string{"a"}, "lxc", "", []string{}},
		{"nodes", []string{"a", " b", "a"}, "lxc", "", []string{"a", "b"}},
		{"nodes", nil, "lxc", "至少选择一个节点", nil},
		{"nodes", []string{"b"}, "podman", "不支持 PODMAN", nil},
		{"nodes", []string{"c"}, "lxc", "对接方式与套餐不一致", nil},
		{"nodes", []string{"h"}, "lxc", "托管节点", nil},
		{"nodes", []string{"r"}, "lxc", "已退役", nil},
		{"nodes", []string{"zzz"}, "lxc", "不存在", nil},
		{"random", nil, "lxc", "节点选择方式无效", nil},
	}
	for _, tc := range cases {
		plan := postgres.Plan{ProviderType: "hatch", Virtualization: tc.virt, NodeSelection: tc.selection, NodeIDs: tc.ids}
		got := validatePlacement(&plan, nodes)
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s %v on %s: %q, want %q", tc.selection, tc.ids, tc.virt, got, tc.want)
			continue
		}
		if tc.want == "" && !slices.Equal(plan.NodeIDs, tc.wantIDs) {
			t.Errorf("%s %v: node ids %v, want %v", tc.selection, tc.ids, plan.NodeIDs, tc.wantIDs)
		}
	}
}
