package strategies_test

import (
	"context"
	"strings"
	"testing"

	"github.com/provasign/astkit"
	"github.com/provasign/astkit/strategies"
)

// extract is a small helper that parses src under lang and runs the
// registered strategy.
func extract(t *testing.T, lang astkit.LanguageKey, src string) ([]astkit.Symbol, []astkit.ImportStatement) {
	t.Helper()
	eng := astkit.NewEngine()
	reg := strategies.Default()
	tree, err := eng.Parse(context.Background(), lang, []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tree == nil {
		t.Fatalf("nil tree for %s", lang)
	}
	defer tree.Close()
	syms, err := reg.Extract(lang, tree, []byte(src))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	imps, err := reg.ExtractImports(lang, tree, []byte(src))
	if err != nil {
		t.Fatalf("imports: %v", err)
	}
	return syms, imps
}

func names(syms []astkit.Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.QualifiedName
	}
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestDefault_RegistersAllLanguages(t *testing.T) {
	reg := strategies.Default()
	for _, l := range []astkit.LanguageKey{
		astkit.LangGo, astkit.LangPython, astkit.LangJava, astkit.LangRust,
		astkit.LangJavaScript, astkit.LangTypeScript, astkit.LangTSX,
		astkit.LangC, astkit.LangCPP, astkit.LangCSharp, astkit.LangPHP,
		astkit.LangSwift, astkit.LangKotlin, astkit.LangObjC,
	} {
		if reg.Get(l) == nil {
			t.Errorf("strategy missing for %s", l)
		}
	}
}

func TestStrategy_Extensions(t *testing.T) {
	reg := strategies.Default()
	cases := map[astkit.LanguageKey]string{
		astkit.LangGo:         ".go",
		astkit.LangPython:     ".py",
		astkit.LangJava:       ".java",
		astkit.LangRust:       ".rs",
		astkit.LangJavaScript: ".js",
		astkit.LangTypeScript: ".ts",
		astkit.LangTSX:        ".tsx",
		astkit.LangC:          ".c",
		astkit.LangCPP:        ".cpp",
		astkit.LangCSharp:     ".cs",
		astkit.LangPHP:        ".php",
		astkit.LangSwift:      ".swift",
		astkit.LangKotlin:     ".kt",
		astkit.LangObjC:       ".m",
	}
	for lang, want := range cases {
		exts := reg.Get(lang).Extensions()
		found := false
		for _, e := range exts {
			if e == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s extensions %v missing %s", lang, exts, want)
		}
	}
}

func TestExtract_Go(t *testing.T) {
	src := `package main

import (
	"fmt"
	"strings"
)

// Hello says hi.
func Hello(name string) string {
	return fmt.Sprintf("hi %s", strings.ToUpper(name))
}

type Greeter struct {
	Prefix string
}

func (g *Greeter) Greet(n string) string {
	return g.Prefix + Hello(n)
}

const Version = "1"
var pkg = "x"
`
	syms, imps := extract(t, astkit.LangGo, src)
	got := names(syms)
	for _, want := range []string{"Hello", "Greeter", "Greet", "Version", "pkg"} {
		if !contains(got, want) {
			t.Errorf("missing symbol %q in %v", want, got)
		}
	}
	if len(imps) != 2 {
		t.Errorf("imports=%d want 2", len(imps))
	}
	for _, i := range imps {
		if i.Group != "stdlib" {
			t.Errorf("expected stdlib group, got %q for %s", i.Group, i.Path)
		}
	}
	// Find Hello and check call-sites + Exported.
	for _, s := range syms {
		if s.QualifiedName == "Hello" {
			if !s.Exported {
				t.Error("Hello must be Exported")
			}
			calls := make(map[string]bool)
			for _, c := range s.CallSites {
				calls[c.Callee] = true
			}
			if !calls["fmt.Sprintf"] && !calls["Sprintf"] {
				t.Errorf("Hello call-sites missing fmt.Sprintf: %v", s.CallSites)
			}
		}
	}
	for _, s := range syms {
		switch s.QualifiedName {
		case "Greeter":
			if !strings.HasPrefix(s.Body, "type Greeter struct") {
				t.Fatalf("Greeter body = %q", s.Body)
			}
		case "Version":
			if !strings.HasPrefix(s.Body, "const Version") {
				t.Fatalf("Version body = %q", s.Body)
			}
		case "pkg":
			if !strings.HasPrefix(s.Body, "var pkg") {
				t.Fatalf("pkg body = %q", s.Body)
			}
		}
	}
}

func TestExtract_GoImportGroups(t *testing.T) {
	src := `package main

import (
	"fmt"
	"github.com/x/y"
	"./local"
)
`
	_, imps := extract(t, astkit.LangGo, src)
	g := map[string]string{}
	for _, i := range imps {
		g[i.Path] = i.Group
	}
	if g["fmt"] != "stdlib" {
		t.Errorf("fmt group=%q", g["fmt"])
	}
	if g["github.com/x/y"] != "external" {
		t.Errorf("external group=%q", g["github.com/x/y"])
	}
	if g["./local"] != "relative" {
		t.Errorf("relative group=%q", g["./local"])
	}
}

func TestExtract_Python(t *testing.T) {
	src := `import os
from pathlib import Path

class Greeter:
    def __init__(self, prefix):
        self.prefix = prefix

    @staticmethod
    def say(n):
        return n

def hello(name):
    return name
`
	syms, imps := extract(t, astkit.LangPython, src)
	got := names(syms)
	for _, w := range []string{"Greeter", "hello"} {
		if !contains(got, w) {
			t.Errorf("missing %q in %v", w, got)
		}
	}
	if len(imps) < 2 {
		t.Errorf("imports=%d want >=2", len(imps))
	}
}

func TestExtract_PythonModuleVarsAndConditionalClasses(t *testing.T) {
	// A module-level annotated global and a class defined only under
	// `if TYPE_CHECKING:` — both invisible before recursion into block
	// statements and expression-statement annotated assignments were added.
	src := `import typing as t
from werkzeug.local import LocalProxy

if t.TYPE_CHECKING:
    from .ctx import _AppCtxGlobals

    class _AppCtxGlobalsProxy(_AppCtxGlobals): ...

g: _AppCtxGlobalsProxy = LocalProxy(_cv_app, "g")

def helper():
    local_only: int = 1
    return local_only
`
	syms, _ := extract(t, astkit.LangPython, src)
	var gVar, proxy *astkit.Symbol
	for i := range syms {
		switch syms[i].Name {
		case "g":
			gVar = &syms[i]
		case "_AppCtxGlobalsProxy":
			proxy = &syms[i]
		case "local_only":
			t.Errorf("function-local annotated var must NOT be indexed as a symbol")
		}
	}
	if gVar == nil {
		t.Fatalf("module-level global `g` not indexed; got %v", names(syms))
	}
	if gVar.Kind != astkit.KindVariable {
		t.Errorf("g kind = %q, want variable", gVar.Kind)
	}
	if !strings.Contains(gVar.Signature, "_AppCtxGlobalsProxy") {
		t.Errorf("g signature %q lost its type annotation", gVar.Signature)
	}
	if proxy == nil || proxy.Kind != astkit.KindClass {
		t.Fatalf("TYPE_CHECKING class `_AppCtxGlobalsProxy` not indexed; got %v", names(syms))
	}
}

func TestExtract_JavaScript(t *testing.T) {
	src := `import {x} from "./mod";

export function hello(name) {
  return x(name);
}

export class A {
  greet(n) { return n; }
}

const Z = () => 1;
`
	syms, imps := extract(t, astkit.LangJavaScript, src)
	got := names(syms)
	for _, w := range []string{"hello", "A"} {
		if !contains(got, w) {
			t.Errorf("missing %q in %v", w, got)
		}
	}
	if len(imps) != 1 || imps[0].Path != "./mod" {
		t.Errorf("imports=%+v", imps)
	}
}

func TestExtract_TypeScript(t *testing.T) {
	src := `import { x } from "./m";

export function add<T extends number>(a: T, b: T): T {
  return (a + b) as T;
}

export interface I { go(): void }
export class C implements I { go() {} }
`
	syms, _ := extract(t, astkit.LangTypeScript, src)
	got := names(syms)
	for _, w := range []string{"add", "C", "I"} {
		if !contains(got, w) {
			t.Errorf("missing %q in %v", w, got)
		}
	}
}

func TestExtract_TSX(t *testing.T) {
	src := `import React from "react";
export const C = (p: {n: string}) => <div>{p.n}</div>;
`
	syms, _ := extract(t, astkit.LangTSX, src)
	if len(syms) == 0 {
		t.Fatal("tsx produced 0 symbols")
	}
}

func TestExtract_Java(t *testing.T) {
	src := `package x;
import java.util.List;

public class Greeter {
  private String prefix;
  public Greeter(String p) { this.prefix = p; }
  @Override public String toString() { return prefix; }
}
`
	syms, imps := extract(t, astkit.LangJava, src)
	got := names(syms)
	if !contains(got, "Greeter") {
		t.Errorf("missing Greeter in %v", got)
	}
	if len(imps) != 1 {
		t.Errorf("imports=%v", imps)
	}
}

// Wrapped declarations (jackson house style) must keep their extends/
// implements clauses and full parameter lists in Signature, and a leading
// annotation must never masquerade as the signature.
func TestExtract_Java_MultilineSignatures(t *testing.T) {
	src := `package x;

@SuppressWarnings("deprecation")
public abstract class StdSerializer<T>
    extends JsonSerializer<T>
    implements JsonFormatVisitable, java.io.Serializable
{
    @Override
    public abstract void serialize(T value, JsonGenerator gen,
        SerializerProvider provider)
        throws IOException;
}
`
	syms, _ := extract(t, astkit.LangJava, src)
	bySig := map[string]string{}
	for _, s := range syms {
		bySig[s.Name] = s.Signature
	}
	clsSig := bySig["StdSerializer"]
	if !strings.Contains(clsSig, "extends JsonSerializer<T>") ||
		!strings.Contains(clsSig, "implements JsonFormatVisitable, java.io.Serializable") {
		t.Errorf("class signature lost wrapped extends/implements: %q", clsSig)
	}
	if strings.Contains(clsSig, "@SuppressWarnings") {
		t.Errorf("annotation leaked into class signature: %q", clsSig)
	}
	mSig := bySig["serialize"]
	if !strings.Contains(mSig, "SerializerProvider provider") {
		t.Errorf("method signature lost wrapped params: %q", mSig)
	}
	if strings.Contains(mSig, "@Override") {
		t.Errorf("annotation leaked into method signature: %q", mSig)
	}
}

func TestExtract_Rust(t *testing.T) {
	src := `use std::fmt;

pub struct Greeter { pub prefix: String }

impl Greeter {
    pub fn new(p: String) -> Self { Self { prefix: p } }
}

pub fn hello(n: &str) -> String { n.to_string() }
`
	syms, imps := extract(t, astkit.LangRust, src)
	got := names(syms)
	if !contains(got, "Greeter") || !contains(got, "hello") {
		t.Errorf("missing in %v", got)
	}
	if len(imps) != 1 {
		t.Errorf("imports=%v", imps)
	}
}

func TestExtract_Swift(t *testing.T) {
	src := `import Foundation

protocol Greeter {
    func greet() -> String
}

public class Person: Greeter {
    public func greet() -> String { return "hi" }
}
`
	syms, imps := extract(t, astkit.LangSwift, src)
	got := names(syms)
	if !contains(got, "Greeter") || !contains(got, "Person") || !contains(got, "greet") {
		t.Errorf("missing in %v", got)
	}
	if len(imps) != 1 || imps[0].Path != "Foundation" {
		t.Errorf("imports=%v", imps)
	}
}

func TestExtract_Kotlin(t *testing.T) {
	src := `package com.example

import kotlin.math.PI

interface Greeter {
    fun greet(): String
}

class Person : Greeter {
    override fun greet(): String = "hi"
}
`
	syms, imps := extract(t, astkit.LangKotlin, src)
	got := names(syms)
	if !contains(got, "Greeter") || !contains(got, "Person") || !contains(got, "greet") {
		t.Errorf("missing in %v", got)
	}
	if len(imps) != 1 || imps[0].Path != "kotlin.math.PI" {
		t.Errorf("imports=%v", imps)
	}
}

func TestExtract_ObjC(t *testing.T) {
	src := `#import <Foundation/Foundation.h>

@protocol Greeter
- (NSString *)greet;
@end

@interface Person : NSObject <Greeter>
- (NSString *)greet;
@end
`
	syms, imps := extract(t, astkit.LangObjC, src)
	got := names(syms)
	if !contains(got, "Greeter") || !contains(got, "Person") || !contains(got, "greet") {
		t.Errorf("missing in %v", got)
	}
	if len(imps) != 1 || imps[0].Path != "Foundation/Foundation.h" {
		t.Errorf("imports=%v", imps)
	}
}

func TestExtract_C(t *testing.T) {
	src := `#include <stdio.h>
#include "x.h"

int add(int a, int b) { return a + b; }
typedef struct Point { int x, y; } Point;
`
	syms, imps := extract(t, astkit.LangC, src)
	got := names(syms)
	if !contains(got, "add") {
		t.Errorf("missing add in %v", got)
	}
	if len(imps) != 2 {
		t.Errorf("imports=%v", imps)
	}
}

func TestExtract_CPP(t *testing.T) {
	src := `#include <vector>
namespace ns {
  class Greeter {
   public:
    Greeter(std::string p) : prefix(p) {}
    std::string greet();
   private:
    std::string prefix;
  };
}
`
	syms, _ := extract(t, astkit.LangCPP, src)
	if len(syms) == 0 {
		t.Fatal("cpp produced 0 symbols")
	}
}

func TestExtract_CPPStructMethods(t *testing.T) {
	src := `struct Widget {
  void inlineMethod() {}
  int multilineMethod()
  {
    return 1;
  }
};`
	syms, _ := extract(t, astkit.LangCPP, src)
	want := map[string]astkit.SymbolKind{
		"Widget":          astkit.KindStruct,
		"inlineMethod":    astkit.KindMethod,
		"multilineMethod": astkit.KindMethod,
	}
	for _, sym := range syms {
		kind, ok := want[sym.Name]
		if !ok {
			continue
		}
		if sym.Kind != kind {
			t.Errorf("%s kind = %s, want %s", sym.Name, sym.Kind, kind)
		}
		if sym.Name != "Widget" && sym.ParentName != "Widget" {
			t.Errorf("%s parent = %q, want Widget", sym.Name, sym.ParentName)
		}
		delete(want, sym.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing C++ struct symbols: %v (all=%v)", want, names(syms))
	}
}

func TestExtract_CPPVisibilityAndFileLocalLinkage(t *testing.T) {
	src := `static void fileLocal() {}
void externallyVisible() {}
class Widget {
public:
  void pubM() {}
protected:
  void protM() {}
private:
  void privM() {}
  void declaredPrivate();
};
void Widget::declaredPrivate() {}
struct Item {
  void publicByDefault() {}
private:
  void privateItem() {}
};`
	syms, _ := extract(t, astkit.LangCPP, src)
	byName := map[string]astkit.Symbol{}
	for _, sym := range syms {
		byName[sym.QualifiedName] = sym
	}
	want := map[string]struct {
		exported bool
		modifier string
	}{
		"fileLocal":              {false, "static"},
		"externallyVisible":      {true, ""},
		"Widget.pubM":            {true, "public"},
		"Widget.protM":           {false, "protected"},
		"Widget.privM":           {false, "private"},
		"Widget.declaredPrivate": {false, "private"},
		"Item.publicByDefault":   {true, "public"},
		"Item.privateItem":       {false, "private"},
	}
	for name, expected := range want {
		sym, ok := byName[name]
		if !ok {
			t.Errorf("missing %s; symbols=%v", name, names(syms))
			continue
		}
		if sym.Exported != expected.exported {
			t.Errorf("%s exported = %v, want %v", name, sym.Exported, expected.exported)
		}
		if expected.modifier != "" && !contains(sym.Modifiers, expected.modifier) {
			t.Errorf("%s modifiers = %v, want %s", name, sym.Modifiers, expected.modifier)
		}
	}
}

func TestExtract_CSharp(t *testing.T) {
	src := `using System;

namespace App {
  public class Greeter {
    public string Prefix { get; set; }
    public Greeter(string p) { Prefix = p; }
    public string Say(string n) => Prefix + n;
  }
}

func TestExtract_CSharpUsingForms(t *testing.T) {
	src := "global using System.IO;\nusing static System.Math;\nusing Alias = Fix.Models.User;\n"
	_, imps := extract(t, astkit.LangCSharp, src)
	got := map[string]bool{}
	for _, imp := range imps {
		got[imp.Path] = true
	}
	for _, want := range []string{"System.IO", "System.Math", "Alias = Fix.Models.User"} {
		if !got[want] {
			t.Errorf("missing normalized C# using %q in %+v", want, imps)
		}
	}
}
`
	syms, imps := extract(t, astkit.LangCSharp, src)
	if len(syms) == 0 {
		t.Fatal("csharp produced 0 symbols")
	}
	if len(imps) != 1 {
		t.Errorf("imports=%v", imps)
	}
}

func TestExtract_PHP(t *testing.T) {
	src := `<?php
namespace App;
use Foo\Bar;

function hello($n) { return $n; }

class Greeter {
  public function __construct(public string $prefix) {}
  public function say($n) { return $this->prefix . $n; }
}
`
	syms, imps := extract(t, astkit.LangPHP, src)
	got := names(syms)
	if !contains(got, "hello") {
		t.Errorf("missing hello in %v", got)
	}
	if len(imps) == 0 {
		t.Errorf("expected imports got %v", imps)
	}
}

func TestExtract_NilTreeReturnsNil(t *testing.T) {
	reg := strategies.Default()
	for _, l := range []astkit.LanguageKey{astkit.LangGo, astkit.LangPython, astkit.LangJava, astkit.LangRust, astkit.LangC, astkit.LangCPP, astkit.LangCSharp, astkit.LangPHP, astkit.LangJavaScript, astkit.LangTypeScript, astkit.LangTSX} {
		s, err := reg.Get(l).Extract(nil, nil)
		if err != nil || s != nil {
			t.Errorf("%s: Extract(nil) returned (%v, %v)", l, s, err)
		}
		i, err := reg.Get(l).ExtractImports(nil, nil)
		if err != nil || i != nil {
			t.Errorf("%s: ExtractImports(nil) returned (%v, %v)", l, i, err)
		}
	}
}

func TestExtract_JSModuleForms(t *testing.T) {
	src := `import direct from "./direct";
const common = require("./common");
async function load() { return import("./dynamic"); }
export * from "./star";
export { Named as Alias } from "./named";
`
	_, imps := extract(t, astkit.LangTypeScript, src)
	got := map[string]bool{}
	for _, imp := range imps {
		got[imp.Path] = true
	}
	for _, want := range []string{"./direct", "./common", "./dynamic", "./star", "./named"} {
		if !got[want] {
			t.Errorf("missing %s import in %#v", want, imps)
		}
	}
}

func TestExtract_TSXComponentUsageIsCallSite(t *testing.T) {
	src := `export function Button() { return <button />; }
export function App() { return <Button />; }
`
	syms, _ := extract(t, astkit.LangTSX, src)
	for _, sym := range syms {
		if sym.Name != "App" {
			continue
		}
		for _, site := range sym.CallSites {
			if site.Callee == "Button" {
				return
			}
		}
		t.Fatalf("App call sites = %+v, want Button", sym.CallSites)
	}
	t.Fatal("App symbol not extracted")
}

func TestExtract_CSharpPropertyBodyIsCallSite(t *testing.T) {
	src := `class Svc {
  int Compute() => 1;
  public int Age => Compute();
}`
	syms, _ := extract(t, astkit.LangCSharp, src)
	for _, sym := range syms {
		if sym.Name != "Age" {
			continue
		}
		for _, site := range sym.CallSites {
			if site.Callee == "Compute" {
				return
			}
		}
		t.Fatalf("Age call sites = %+v, want Compute", sym.CallSites)
	}
	t.Fatal("Age property symbol not extracted")
}

func TestExtract_Signature(t *testing.T) {
	syms, _ := extract(t, astkit.LangGo, "package x\nfunc Foo(a int, b string) (string, error) { return \"\", nil }\n")
	for _, s := range syms {
		if s.QualifiedName == "Foo" {
			if !strings.Contains(s.Signature, "Foo") || strings.Contains(s.Signature, "{") {
				t.Errorf("signature wrong: %q", s.Signature)
			}
			return
		}
	}
	t.Fatal("Foo not found")
}

func TestJava_LombokAccessorSynthesis(t *testing.T) {
	src := []byte(`
import lombok.Getter;
import lombok.Setter;
import lombok.Data;

@Getter
@Setter
public class LoanMapping {
    private Long loanId;
    private boolean active;
}

@Data
class Wallet {
    private String owner;
}

class Plain {
    private int x;
}

@Getter
class Explicit {
    private String name;
    public String getName() { return name; }
}
`)
	eng := astkit.NewEngine()
	tree, err := eng.Parse(context.Background(), astkit.LangJava, src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	syms, err := strategies.NewJava().Extract(tree, src)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]astkit.Symbol{}
	for _, s := range syms {
		byName[s.QualifiedName] = s
	}
	for _, want := range []string{"LoanMapping.getLoanId", "LoanMapping.setLoanId",
		"LoanMapping.isActive", "LoanMapping.setActive",
		"Wallet.getOwner", "Wallet.setOwner"} {
		s, ok := byName[want]
		if !ok {
			t.Errorf("missing synthesized accessor %s", want)
			continue
		}
		if s.Kind != astkit.KindMethod || len(s.Modifiers) == 0 || s.Modifiers[0] != "lombok-generated" {
			t.Errorf("%s: kind=%s modifiers=%v", want, s.Kind, s.Modifiers)
		}
	}
	if _, ok := byName["Plain.getX"]; ok {
		t.Error("Plain has no Lombok annotations but got a synthesized getter")
	}
	var explicitGetters int
	for _, s := range syms {
		if s.QualifiedName == "Explicit.getName" {
			explicitGetters++
			if len(s.Modifiers) > 0 && s.Modifiers[0] == "lombok-generated" {
				t.Error("explicit getter was replaced by Lombok synthesis")
			}
		}
	}
	if explicitGetters != 1 {
		t.Errorf("Explicit.getName count=%d want 1", explicitGetters)
	}
}

func TestPython_ClassAttributesAsFields(t *testing.T) {
	src := []byte(`
import sqlalchemy.orm as so

class User:
    plain = 5
    annotated: int = 7
    bare: str
    username: so.Mapped[str] = so.mapped_column()
    _private: int = 0

g: SomeProxy = make_proxy()
plain_global = 3
`)
	eng := astkit.NewEngine()
	tree, err := eng.Parse(context.Background(), astkit.LangPython, src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	syms, err := strategies.NewPython().Extract(tree, src)
	if err != nil {
		t.Fatal(err)
	}
	byQN := map[string]astkit.Symbol{}
	for _, s := range syms {
		byQN[s.QualifiedName] = s
	}
	for _, want := range []string{"User.plain", "User.annotated", "User.bare", "User.username", "User._private"} {
		s, ok := byQN[want]
		if !ok {
			t.Errorf("missing class attribute %s (have %v)", want, keysOf(byQN))
			continue
		}
		if s.Kind != astkit.KindField || s.ParentName != "User" {
			t.Errorf("%s: kind=%s parent=%s", want, s.Kind, s.ParentName)
		}
	}
	if byQN["User._private"].Exported {
		t.Error("_private should not be exported")
	}
	// Module globals: annotated and plain are both indexed (plain ones since
	// 2026-09-26, see pythonModuleAssignments).
	if _, ok := byQN["g"]; !ok {
		t.Error("annotated module global lost")
	}
	if _, ok := byQN["plain_global"]; !ok {
		t.Error("plain module global not indexed")
	}
}

func TestCSharpTopLevelStatementsAndLocalFunction(t *testing.T) {
	src := []byte(`Console.WriteLine(TopHelper());
static int TopHelper() { return Math.Abs(-1); }
`)
	eng := astkit.NewEngine()
	tree, err := eng.Parse(context.Background(), astkit.LangCSharp, src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	syms, err := strategies.NewCSharp().Extract(tree, src)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]astkit.Symbol{}
	for _, s := range syms {
		got[s.Name] = s
	}
	if got["TopHelper"].Kind != astkit.KindFunction {
		t.Fatalf("TopHelper=%+v", got["TopHelper"])
	}
	entry, ok := got["<top-level>"]
	if !ok || len(entry.CallSites) == 0 {
		t.Fatalf("top-level entry=%+v", entry)
	}
	for _, cs := range entry.CallSites {
		if cs.Callee == "Math.Abs" {
			t.Fatal("local-function body call leaked into top-level entry")
		}
	}
}

func keysOf(m map[string]astkit.Symbol) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// `new Outer(this)` records "this" as the argument (the keyword is its own
// grammar node, not an identifier node), so a caller can type it against
// the enclosing class — commons-io's `new WildcardFileFilter(this)` inside
// its Builder.get().
func TestExtract_Java_ThisArgumentToken(t *testing.T) {
	src := `package x;
public class Outer {
  public static class Builder {
    public Outer get() { return new Outer(this); }
  }
}
`
	syms, _ := extract(t, astkit.LangJava, src)
	for _, s := range syms {
		if s.Name != "get" {
			continue
		}
		if len(s.CallSites) != 1 || len(s.CallSites[0].Args) != 1 || s.CallSites[0].Args[0] != "this" {
			t.Fatalf("get() call sites = %+v", s.CallSites)
		}
		return
	}
	t.Fatalf("get() not extracted: %+v", syms)
}

// TestExtract_GoStructFields: named struct fields are KindField symbols
// parented to their struct (gin Context.Errors was unfindable by name,
// 2026-09-25). Multi-name declarations yield one symbol each; embedded
// fields and struct tags stay out.
func TestExtract_GoStructFields(t *testing.T) {
	src := "package p\n\ntype Context struct {\n\tsync.Mutex\n\t// Errors is a list of errors.\n\tErrors errorMsgs `json:\"errors\"`\n\tA, b int\n}\n\ntype Alias int\n"
	syms, _ := extract(t, astkit.LangGo, src)
	fields := map[string]astkit.Symbol{}
	for _, s := range syms {
		if s.Kind == astkit.KindField {
			fields[s.Name] = s
		}
	}
	if len(fields) != 3 {
		t.Fatalf("want fields Errors, A, b; got %v", fields)
	}
	e := fields["Errors"]
	if e.ParentName != "Context" || e.Signature != "Errors errorMsgs" || !e.Exported || e.Span.Start != 6 {
		t.Errorf("Errors field = %+v", e)
	}
	if fields["A"].Signature != "A int" || fields["b"].Exported {
		t.Errorf("multi-name fields = %+v %+v", fields["A"], fields["b"])
	}
}

// TestExtract_CMembersAndFileVars: C/C++ struct members and file-scope
// variables are indexed; functions returning pointers stay functions, and a
// function-pointer member is a field (declaration-coverage gaps, 2026-09-26).
func TestExtract_CMembersAndFileVars(t *testing.T) {
	src := "static int count = 0;\nextern int elsewhere;\njson_t *json_null(void);\n" +
		"struct s { int a, *b; char name[8]; int (*cb)(int); };\n" +
		"typedef struct { double x; } pt;\n"
	syms, _ := extract(t, astkit.LangC, src)
	kinds := map[string]astkit.SymbolKind{}
	for _, s := range syms {
		kinds[s.ParentName+"."+s.Name] = s.Kind
	}
	want := map[string]astkit.SymbolKind{
		".count": astkit.KindVariable,
		"s.a": astkit.KindField, "s.b": astkit.KindField, "s.name": astkit.KindField,
		"s.cb": astkit.KindField, "pt.x": astkit.KindField,
	}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("%s: got %q want %q (all %v)", k, kinds[k], v, kinds)
		}
	}
	// A prototype returning a pointer is not a variable. (It is not indexed as a
	// function either -- a separate, pre-existing gap whose fix moves call
	// resolution between header prototypes and definitions; left for its own
	// change.)
	if kinds[".json_null"] == astkit.KindVariable {
		t.Errorf("pointer-returning prototype indexed as a variable: %v", kinds)
	}
	if _, ok := kinds[".elsewhere"]; ok {
		t.Errorf("extern declaration indexed as a variable: %v", kinds)
	}

	cpp := "class C {\npublic:\n  int *get();\n  int &ref();\n  int size_;\n  static const int kMax = 3;\n};\n"
	syms, _ = extract(t, astkit.LangCPP, cpp)
	kinds = map[string]astkit.SymbolKind{}
	for _, s := range syms {
		kinds[s.QualifiedName] = s.Kind
	}
	if kinds["C.get"] == astkit.KindField || kinds["C.ref"] == astkit.KindField {
		t.Errorf("method prototype returning a pointer/reference indexed as a field: %v", kinds)
	}
	if kinds["C.size_"] != astkit.KindField || kinds["C.kMax"] != astkit.KindField {
		t.Errorf("C++ members: %v", kinds)
	}
}

// TestExtract_PHPPropertiesAndJSValues covers the other two gaps.
func TestExtract_PHPPropertiesAndJSValues(t *testing.T) {
	php := "<?php\nconst TOP = 1;\nclass S { public int $a = 1, $b; const K = 2; }\n"
	syms, _ := extract(t, astkit.LangPHP, php)
	kinds := map[string]astkit.SymbolKind{}
	for _, s := range syms {
		kinds[s.ParentName+"."+s.Name] = s.Kind
	}
	for k, v := range map[string]astkit.SymbolKind{".TOP": astkit.KindConst, "S.a": astkit.KindField, "S.b": astkit.KindField, "S.K": astkit.KindConst} {
		if kinds[k] != v {
			t.Errorf("php %s: got %q want %q (all %v)", k, kinds[k], v, kinds)
		}
	}
	js := "const fs = require('fs');\nconst { a, b } = obj;\nexport const LIMIT = 10;\nlet ready = false;\nconst run = () => 1;\n"
	syms, _ = extract(t, astkit.LangJavaScript, js)
	kinds = map[string]astkit.SymbolKind{}
	for _, s := range syms {
		kinds[s.Name] = s.Kind
	}
	if kinds["LIMIT"] != astkit.KindVariable || kinds["ready"] != astkit.KindVariable || kinds["run"] != astkit.KindFunction {
		t.Errorf("js values: %v", kinds)
	}
	for _, n := range []string{"fs", "a", "b"} {
		if _, ok := kinds[n]; ok {
			t.Errorf("js %s should not be indexed (require/destructuring): %v", n, kinds)
		}
	}
}
