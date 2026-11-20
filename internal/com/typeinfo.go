//go:build windows && (amd64 || arm64)

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

package com

import (
	"fmt"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

// Reading the type library behind an IDispatch object: member names and
// signatures exactly as the running application declares them. Used to find
// out how an API looks in a particular CorelDRAW version.

type iTypeInfo struct{ vtbl *typeInfoVtbl }

type typeInfoVtbl struct {
	QueryInterface       uintptr
	AddRef               uintptr
	Release              uintptr
	GetTypeAttr          uintptr
	GetTypeComp          uintptr
	GetFuncDesc          uintptr
	GetVarDesc           uintptr
	GetNames             uintptr
	GetRefTypeOfImplType uintptr
	GetImplTypeFlags     uintptr
	GetIDsOfNames        uintptr
	Invoke               uintptr
	GetDocumentation     uintptr
	GetDllEntry          uintptr
	GetRefTypeInfo       uintptr
	AddressOfMember      uintptr
	CreateInstance       uintptr
	GetMops              uintptr
	GetContainingTypeLib uintptr
	ReleaseTypeAttr      uintptr
	ReleaseFuncDesc      uintptr
	ReleaseVarDesc       uintptr
}

// typeAttr – only the leading fields we need (TYPEATTR, 64-bit layout).
type typeAttr struct {
	guid             GUID
	lcid             uint32
	dwReserved       uint32
	memidConstructor int32
	memidDestructor  int32
	lpstrSchema      uintptr
	cbSizeInstance   uint32
	typekind         int32
	cFuncs           uint16
	cVars            uint16
	cImplTypes       uint16
	cbSizeVft        uint16
	cbAlignment      uint16
	wTypeFlags       uint16
	wMajorVerNum     uint16
	wMinorVerNum     uint16
}

// These structs are only ever read from memory owned by COM (never allocated
// by Go), so pointer-typed fields are safe even when the union holds a number.
type typeDesc struct {
	ptr unsafe.Pointer // lptdesc / lpadesc / hreftype
	vt  uint16
	_   [6]byte
}

type paramDesc struct {
	pparamdescex uintptr
	wParamFlags  uint16
	_            [6]byte
}

type elemDesc struct {
	tdesc typeDesc
	pdesc paramDesc
}

type funcDesc struct {
	memid             int32
	_                 int32
	lprgscode         uintptr
	lprgelemdescParam unsafe.Pointer
	funckind          int32
	invkind           int32
	callconv          int32
	cParams           int16
	cParamsOpt        int16
	oVft              int16
	cScodes           int16
	_                 int32
	elemdescFunc      elemDesc
	wFuncFlags        uint16
}

const (
	paramFlagIn       = 0x1
	paramFlagOut      = 0x2
	paramFlagOpt      = 0x10
	paramFlagHasDeflt = 0x20

	invokeFunc          = 1
	invokePropertyGet   = 2
	invokePropertyPut   = 4
	invokePropertyPutRf = 8

	vtPtr      = 26
	vtSafeArr  = 27
	vtCArray   = 28
	vtUserDef  = 29
	vtHRESULT  = 25
	vtVoid     = 24
	vtVariantT = 12
)

var vtNames = map[uint16]string{
	0: "Empty", 2: "Integer", 3: "Long", 4: "Single", 5: "Double", 6: "Currency", 7: "Date",
	8: "String", 9: "Object", 10: "Error", 11: "Boolean", 12: "Variant", 13: "IUnknown",
	14: "Decimal", 16: "SByte", 17: "Byte", 18: "UInt16", 19: "UInt32", 20: "LongLong",
	21: "ULongLong", 22: "Int", 23: "UInt", 24: "void", 25: "HRESULT", 26: "Ptr", 27: "SafeArray",
	28: "CArray", 29: "UserDefined", 30: "LPSTR", 31: "LPWSTR",
}

func (t *iTypeInfo) release() { syscall.SyscallN(t.vtbl.Release, uintptr(unsafe.Pointer(t))) }

func (t *iTypeInfo) refName(href uintptr) string {
	var ref *iTypeInfo
	hr, _, _ := syscall.SyscallN(t.vtbl.GetRefTypeInfo, uintptr(unsafe.Pointer(t)), href, uintptr(unsafe.Pointer(&ref)))
	if failed(hr) || ref == nil {
		return "UserDefined"
	}
	defer ref.release()
	return ref.docName(-1)
}

func (t *iTypeInfo) docName(memid int32) string {
	var name *uint16
	hr, _, _ := syscall.SyscallN(t.vtbl.GetDocumentation, uintptr(unsafe.Pointer(t)), uintptr(memid),
		uintptr(unsafe.Pointer(&name)), 0, 0, 0)
	if failed(hr) {
		return "?"
	}
	s := bstrToString(name)
	freeBSTR(name)
	return s
}

func (t *iTypeInfo) typeName(td *typeDesc) string {
	switch td.vt {
	case vtPtr:
		return t.typeName((*typeDesc)(td.ptr))
	case vtSafeArr:
		return t.typeName((*typeDesc)(td.ptr)) + "()"
	case vtUserDef:
		return t.refName(uintptr(td.ptr))
	}
	if n, ok := vtNames[td.vt]; ok {
		return n
	}
	return fmt.Sprintf("VT_%d", td.vt)
}

func (d *Dispatch) typeInfo() (*iTypeInfo, error) {
	if d == nil || d.p == nil {
		return nil, fmt.Errorf("object is Nothing")
	}
	var ti *iTypeInfo
	hr, _, _ := syscall.SyscallN(d.p.vtbl.GetTypeInfo, uintptr(unsafe.Pointer(d.p)), 0, lcidEnUS,
		uintptr(unsafe.Pointer(&ti)))
	if failed(hr) || ti == nil {
		return nil, &Error{Op: "GetTypeInfo", HRESULT: uint32(hr)}
	}
	return ti, nil
}

// baseMembers are IUnknown / IDispatch methods listed by dual interfaces – noise.
var baseMembers = map[string]bool{"QueryInterface": true, "AddRef": true, "Release": true,
	"GetTypeInfoCount": true, "GetTypeInfo": true, "GetIDsOfNames": true, "Invoke": true}

// Member is one method or property as declared in the type library.
type Member struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"` // method | get | put | putref
	Signature string `json:"signature"`
}

// Describe lists the members of the object's interface (filter: case-insensitive
// substring of the member name; empty = all).
func (d *Dispatch) Describe(filter string) (typeName string, members []Member, err error) {
	ti, err := d.typeInfo()
	if err != nil {
		return "", nil, err
	}
	defer ti.release()
	return ti.describe(filter)
}

// DescribeType lists the members of an interface found by name (e.g. "IVGTextRange")
// in the type library that defines the object's interface.
func (d *Dispatch) DescribeType(typeName, filter string) (string, []Member, error) {
	ti, err := d.findType(typeName)
	if err != nil {
		return "", nil, err
	}
	defer ti.release()
	return ti.describe(filter)
}

func (ti *iTypeInfo) describe(filter string) (typeName string, members []Member, err error) {
	typeName = ti.docName(-1)
	var ta *typeAttr
	hr, _, _ := syscall.SyscallN(ti.vtbl.GetTypeAttr, uintptr(unsafe.Pointer(ti)), uintptr(unsafe.Pointer(&ta)))
	if failed(hr) || ta == nil {
		return typeName, nil, &Error{Op: "GetTypeAttr", HRESULT: uint32(hr)}
	}
	nFuncs := int(ta.cFuncs)
	syscall.SyscallN(ti.vtbl.ReleaseTypeAttr, uintptr(unsafe.Pointer(ti)), uintptr(unsafe.Pointer(ta)))
	filter = strings.ToLower(filter)
	for i := 0; i < nFuncs; i++ {
		var fd *funcDesc
		hr, _, _ := syscall.SyscallN(ti.vtbl.GetFuncDesc, uintptr(unsafe.Pointer(ti)), uintptr(i), uintptr(unsafe.Pointer(&fd)))
		if failed(hr) || fd == nil {
			continue
		}
		m, ok := ti.member(fd, filter)
		syscall.SyscallN(ti.vtbl.ReleaseFuncDesc, uintptr(unsafe.Pointer(ti)), uintptr(unsafe.Pointer(fd)))
		if ok {
			members = append(members, m)
		}
	}
	sort.SliceStable(members, func(a, b int) bool { return members[a].Name < members[b].Name })
	return typeName, members, nil
}

func (ti *iTypeInfo) member(fd *funcDesc, filter string) (Member, bool) {
	n := int(fd.cParams)
	names := make([]*uint16, n+1)
	var got uint32
	syscall.SyscallN(ti.vtbl.GetNames, uintptr(unsafe.Pointer(ti)), uintptr(fd.memid),
		uintptr(unsafe.Pointer(&names[0])), uintptr(n+1), uintptr(unsafe.Pointer(&got)))
	strs := make([]string, len(names))
	for i, p := range names[:got] {
		strs[i] = bstrToString(p)
		freeBSTR(p)
	}
	m := Member{Name: strs[0]}
	if strings.HasPrefix(m.Name, "_") || baseMembers[m.Name] || (filter != "" && !strings.Contains(strings.ToLower(m.Name), filter)) {
		return m, false
	}
	switch fd.invkind {
	case invokeFunc:
		m.Kind = "method"
	case invokePropertyGet:
		m.Kind = "get"
	case invokePropertyPut:
		m.Kind = "put"
	case invokePropertyPutRf:
		m.Kind = "putref"
	}
	var params []string
	elems := unsafe.Slice((*elemDesc)(fd.lprgelemdescParam), n)
	for i := 0; i < n; i++ {
		pn := fmt.Sprintf("p%d", i+1)
		if i+1 < len(strs) && strs[i+1] != "" {
			pn = strs[i+1]
		}
		e := &elems[i]
		s := pn + " As " + ti.typeName(&e.tdesc)
		flags := e.pdesc.wParamFlags
		if flags&paramFlagOut != 0 && flags&paramFlagIn == 0 {
			s = "out " + s
		}
		if flags&paramFlagOpt != 0 || flags&paramFlagHasDeflt != 0 || i >= n-int(fd.cParamsOpt) && fd.cParamsOpt > 0 {
			s = "[" + s + "]"
		}
		params = append(params, s)
	}
	ret := ti.typeName(&fd.elemdescFunc.tdesc)
	m.Signature = m.Name + "(" + strings.Join(params, ", ") + ")"
	if ret != "void" && ret != "HRESULT" {
		m.Signature += " As " + ret
	}
	return m, true
}

// ---- enumerations (type library) ------------------------------------------------

type iTypeLib struct{ vtbl *typeLibVtbl }

type typeLibVtbl struct {
	QueryInterface    uintptr
	AddRef            uintptr
	Release           uintptr
	GetTypeInfoCount  uintptr
	GetTypeInfo       uintptr
	GetTypeInfoType   uintptr
	GetTypeInfoOfGuid uintptr
	GetLibAttr        uintptr
	GetTypeComp       uintptr
	GetDocumentation  uintptr
	IsName            uintptr
	FindName          uintptr
	ReleaseTLibAttr   uintptr
}

type varDesc struct {
	memid       int32
	_           int32
	lpstrSchema uintptr
	value       unsafe.Pointer // oInst or lpvarValue (*VARIANT) for constants
	elemdescVar elemDesc
	wVarFlags   uint16
	_           uint16
	varkind     int32
}

const varkindConst = 2

// EnumConst is one constant of an enumeration.
type EnumConst struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

// findType finds a type (interface, enum …) by name in the type library that
// defines the object's interface.
func (d *Dispatch) findType(name string) (*iTypeInfo, error) {
	ti, err := d.typeInfo()
	if err != nil {
		return nil, err
	}
	defer ti.release()
	var tl *iTypeLib
	var idx uint32
	hr, _, _ := syscall.SyscallN(ti.vtbl.GetContainingTypeLib, uintptr(unsafe.Pointer(ti)),
		uintptr(unsafe.Pointer(&tl)), uintptr(unsafe.Pointer(&idx)))
	if failed(hr) || tl == nil {
		return nil, &Error{Op: "GetContainingTypeLib", HRESULT: uint32(hr)}
	}
	defer syscall.SyscallN(tl.vtbl.Release, uintptr(unsafe.Pointer(tl)))
	n, _, _ := syscall.SyscallN(tl.vtbl.GetTypeInfoCount, uintptr(unsafe.Pointer(tl)))
	for i := uintptr(0); i < n; i++ {
		var bname *uint16
		hr, _, _ := syscall.SyscallN(tl.vtbl.GetDocumentation, uintptr(unsafe.Pointer(tl)), i,
			uintptr(unsafe.Pointer(&bname)), 0, 0, 0)
		if failed(hr) {
			continue
		}
		tn := bstrToString(bname)
		freeBSTR(bname)
		if !strings.EqualFold(tn, name) {
			continue
		}
		var found *iTypeInfo
		hr, _, _ = syscall.SyscallN(tl.vtbl.GetTypeInfo, uintptr(unsafe.Pointer(tl)), i, uintptr(unsafe.Pointer(&found)))
		if failed(hr) || found == nil {
			return nil, &Error{Op: "GetTypeInfo " + name, HRESULT: uint32(hr)}
		}
		return found, nil
	}
	return nil, fmt.Errorf("type %q not found in the type library", name)
}

// Enum reads the constants of an enumeration (e.g. "cdrFilter") from the type
// library that defines the object's interface. filter: substring of constant names.
func (d *Dispatch) Enum(enumName, filter string) ([]EnumConst, error) {
	eti, err := d.findType(enumName)
	if err != nil {
		return nil, err
	}
	defer eti.release()
	var ta *typeAttr
	hr, _, _ := syscall.SyscallN(eti.vtbl.GetTypeAttr, uintptr(unsafe.Pointer(eti)), uintptr(unsafe.Pointer(&ta)))
	if failed(hr) || ta == nil {
		return nil, &Error{Op: "GetTypeAttr " + enumName, HRESULT: uint32(hr)}
	}
	nVars := int(ta.cVars)
	syscall.SyscallN(eti.vtbl.ReleaseTypeAttr, uintptr(unsafe.Pointer(eti)), uintptr(unsafe.Pointer(ta)))
	filter = strings.ToLower(filter)
	var out []EnumConst
	for j := 0; j < nVars; j++ {
		var vd *varDesc
		hr, _, _ := syscall.SyscallN(eti.vtbl.GetVarDesc, uintptr(unsafe.Pointer(eti)), uintptr(j), uintptr(unsafe.Pointer(&vd)))
		if failed(hr) || vd == nil {
			continue
		}
		c := EnumConst{Name: eti.docName(vd.memid)}
		if vd.varkind == varkindConst && vd.value != nil {
			v := *(*variant)(vd.value)
			if x, ok := ToInt(variantNumber(&v)); ok {
				c.Value = x
			}
		}
		syscall.SyscallN(eti.vtbl.ReleaseVarDesc, uintptr(unsafe.Pointer(eti)), uintptr(unsafe.Pointer(vd)))
		if filter == "" || strings.Contains(strings.ToLower(c.Name), filter) {
			out = append(out, c)
		}
	}
	return out, nil
}

// variantNumber reads a numeric VARIANT without taking ownership (no clearing).
func variantNumber(v *variant) any {
	switch v.vt {
	case vtI2:
		return int64(int16(v.val))
	case vtI4, vtInt:
		return int64(int32(v.val))
	case vtUI1:
		return int64(uint8(v.val))
	case vtUI2:
		return int64(uint16(v.val))
	case vtUI4, vtUint:
		return int64(uint32(v.val))
	case vtI8, vtUI8:
		return v.val
	}
	return nil
}
