package apiargs

import (
	"reflect"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestLocateArgsForms(t *testing.T) {
	argv := []string{"api", "-ifbody=old", "--field=title=new", "--input", "payload", "repos/o/r/issues/1", "--header=X:Y"}
	if _, err := Parse(argv[1:]); err != nil {
		t.Fatal(err)
	}
	got := LocateArgs(argv)
	if got.Endpoint != 5 {
		t.Fatalf("endpoint %d", got.Endpoint)
	}
	want := []FieldArg{
		{Key: "body", Value: "old", Span: model.ArgSpan{Start: 1, End: 2, Keep: []string{"-i"}}},
		{Key: "title", Value: "new", Typed: true, Span: model.ArgSpan{Start: 2, End: 3}},
	}
	if !reflect.DeepEqual(got.Fields, want) {
		t.Fatalf("fields %#v", got.Fields)
	}
	if !reflect.DeepEqual(got.Input, []model.ArgSpan{{Start: 3, End: 5}}) {
		t.Fatalf("input %#v", got.Input)
	}
}

func TestLocateArgsTerminatesFlags(t *testing.T) {
	got := LocateArgs([]string{"api", "-XPOST", "--", "repos/o/r/issues/1"})
	if got.Endpoint != 3 || len(got.Fields) != 0 {
		t.Fatalf("locators %#v", got)
	}
}
