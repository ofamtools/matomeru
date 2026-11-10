package store

import (
	"fmt"
	"testing"
)

func TestQueryCompoundPredicatesKeepBoundValuesAndPage(t *testing.T) {
	t.Parallel()
	db := queryFixture(t)
	q := Query{Where: []QueryCond{{Any: []QueryCond{
		{Fields: []string{"name"}, Op: OpEq, Value: "' OR 1=1 --"},
		{All: []QueryCond{
			{Fields: []string{"n"}, Op: OpIn, Value: []int{1, 2, 3, 10}},
			{Fields: []string{"archived"}, Op: OpEq, Value: false},
		}},
	}}}, Sort: []QuerySort{{Fields: []string{"n"}}}, Limit: 2, Offset: 1}
	got, err := db.Query("things", q)
	if err != nil || got.Total != 3 || fmt.Sprint(ids(t, got)) != "[t002 t003]" {
		t.Fatalf("compound page: %+v %v", got, err)
	}
	for _, tc := range []struct {
		c    QueryCond
		want int
	}{
		{QueryCond{Any: []QueryCond{}}, 0},
		{QueryCond{All: []QueryCond{}}, 100},
		{QueryCond{Not: true, Any: []QueryCond{{Fields: []string{"n"}, Op: OpLt, Value: 99}}}, 1},
	} {
		got, err := db.Query("things", Query{Where: []QueryCond{tc.c}})
		if err != nil || got.Total != tc.want {
			t.Fatalf("%+v: total %d, error %v, want %d", tc.c, got.Total, err, tc.want)
		}
	}
}
