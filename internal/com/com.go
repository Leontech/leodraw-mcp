//go:build windows && (amd64 || arm64)

// LeoDraw MCP – MCP server for CorelDRAW
// Copyright (C) 2026 Leoš Vilím <https://leotech.cz>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package com is a small, dependency-free COM automation (IDispatch) client
// built directly on ole32/oleaut32 via syscall.
//
// All objects must be used only on the worker thread (see Worker): COM
// objects obtained in an STA belong to that thread.
package com

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	modole32    = syscall.NewLazyDLL("ole32.dll")
	modoleaut32 = syscall.NewLazyDLL("oleaut32.dll")
	moduser32   = syscall.NewLazyDLL("user32.dll")

	procCoInitializeEx   = modole32.NewProc("CoInitializeEx")
	procCoUninitialize   = modole32.NewProc("CoUninitialize")
	procCLSIDFromProgID  = modole32.NewProc("CLSIDFromProgID")
	procCoCreateInstance = modole32.NewProc("CoCreateInstance")
	procGetActiveObject  = modoleaut32.NewProc("GetActiveObject")
	procSysAllocString   = modoleaut32.NewProc("SysAllocString")
	procSysFreeString    = modoleaut32.NewProc("SysFreeString")
	procSysStringLen     = modoleaut32.NewProc("SysStringLen")
	procVariantClear     = modoleaut32.NewProc("VariantClear")
	procPeekMessageW     = moduser32.NewProc("PeekMessageW")
	procTranslateMessage = moduser32.NewProc("TranslateMessage")
	procDispatchMessageW = moduser32.NewProc("DispatchMessageW")
)

// GUID is a Windows GUID.
type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	iidNull      GUID
	iidIUnknown  = GUID{0x00000000, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDispatch = GUID{0x00020400, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
)

const (
	coinitApartmentThreaded = 0x2
	clsctxInprocServer      = 0x1
	clsctxLocalServer       = 0x4

	dispatchMethod         = 0x1
	dispatchPropertyGet    = 0x2
	dispatchPropertyPut    = 0x4
	dispatchPropertyPutRef = 0x8

	dispidPropertyPut = -3

	// en-US: keeps any implicit number/string coercion independent of the
	// Windows locale (Czech uses a decimal comma).
	lcidEnUS = 0x0409

	maxBusyRetries = 20
)

// HRESULT values we care about.
const (
	sFalse                  = 0x00000001
	rpcEChangedMode         = 0x80010106
	eInvalidArg             = 0x80070057
	eFail                   = 0x80004005
	eNoInterface            = 0x80004002
	dispEUnknownName        = 0x80020006
	dispEMemberNotFound     = 0x80020003
	dispEParamNotFound      = 0x80020004
	dispETypeMismatch       = 0x80020005
	dispEException          = 0x80020009
	dispEBadParamCount      = 0x8002000E
	dispEOverflow           = 0x8002000A
	mkEUnavailable          = 0x800401E3
	coEClassString          = 0x800401F3
	regdbEClassNotReg       = 0x80040154
	rpcECallRejected        = 0x80010001
	rpcEServerCallRetryLate = 0x8001010A
	rpcEDisconnected        = 0x80010108
	rpcSServerUnavailable   = 0x800706BA
	rpcSCallFailed          = 0x800706BE
	coEObjNotConnected      = 0x800401FD
)

// Error is a failed COM call.
type Error struct {
	Op          string // e.g. "Invoke ActiveDocument"
	HRESULT     uint32
	Source      string
	Description string
}

func (e *Error) Error() string {
	msg := e.Description
	if msg == "" {
		msg = hresultText(e.HRESULT)
	}
	if e.Source != "" {
		msg = e.Source + ": " + msg
	}
	return fmt.Sprintf("%s failed: %s (0x%08X)", e.Op, msg, e.HRESULT)
}

func hresultText(hr uint32) string {
	switch hr {
	case eInvalidArg:
		return "invalid argument"
	case eFail:
		return "unspecified failure"
	case eNoInterface:
		return "interface not supported"
	case dispEUnknownName:
		return "unknown member name"
	case dispEMemberNotFound:
		return "member not found"
	case dispEParamNotFound:
		return "required parameter missing"
	case dispETypeMismatch:
		return "type mismatch"
	case dispEBadParamCount:
		return "wrong number of parameters"
	case dispEOverflow:
		return "value out of range"
	case mkEUnavailable:
		return "application is not running"
	case coEClassString, regdbEClassNotReg:
		return "application is not installed (class not registered)"
	case rpcECallRejected, rpcEServerCallRetryLate:
		return "application is busy (is a dialog open?)"
	case rpcEDisconnected, rpcSServerUnavailable, rpcSCallFailed, coEObjNotConnected:
		return "application was closed or disconnected"
	}
	return "COM error"
}

// HResult returns the HRESULT of err if it is a *Error, else 0.
func HResult(err error) uint32 {
	var e *Error
	if errors.As(err, &e) {
		return e.HRESULT
	}
	return 0
}

// IsDisconnected reports whether err means the server process is gone.
func IsDisconnected(err error) bool {
	switch HResult(err) {
	case rpcEDisconnected, rpcSServerUnavailable, rpcSCallFailed, coEObjNotConnected:
		return true
	}
	return false
}

// IsNotRunning reports whether err means the application is not running or not installed.
func IsNotRunning(err error) bool {
	switch HResult(err) {
	case mkEUnavailable, coEClassString, regdbEClassNotReg:
		return true
	}
	return false
}

func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

// ---- VARIANT ----------------------------------------------------------------

const (
	vtEmpty    = 0
	vtNull     = 1
	vtI2       = 2
	vtI4       = 3
	vtR4       = 4
	vtR8       = 5
	vtCY       = 6
	vtDate     = 7
	vtBSTR     = 8
	vtDispatch = 9
	vtError    = 10
	vtBool     = 11
	vtUnknown  = 13
	vtI1       = 16
	vtUI1      = 17
	vtUI2      = 18
	vtUI4      = 19
	vtI8       = 20
	vtUI8      = 21
	vtInt      = 22
	vtUint     = 23
)

// variant matches the 24-byte VARIANT layout on 64-bit Windows.
type variant struct {
	vt  uint16
	r1  uint16
	r2  uint16
	r3  uint16
	val int64
	_   int64
}

type dispParams struct {
	rgvarg            *variant
	rgdispidNamedArgs *int32
	cArgs             uint32
	cNamedArgs        uint32
}

type excepInfo struct {
	wCode             uint16
	wReserved         uint16
	bstrSource        *uint16
	bstrDescription   *uint16
	bstrHelpFile      *uint16
	dwHelpContext     uint32
	pvReserved        uintptr
	pfnDeferredFillIn uintptr
	scode             int32
}

type missingArg struct{}

// Missing stands for an omitted optional argument (VT_ERROR / DISP_E_PARAMNOTFOUND).
// Only usable for optional VARIANT parameters: CorelDRAW rejects it (type
// mismatch) for typed optional parameters – pass a real value or Nothing.
var Missing = missingArg{}

// Nothing is a null object reference (VT_DISPATCH NULL), e.g. for optional
// object parameters in late-bound calls.
var Nothing = (*Dispatch)(nil)

// Unsupported is returned for VARIANT types this package does not convert (e.g. arrays).
type Unsupported struct{ VT uint16 }

func sysAllocString(s string) (uintptr, error) {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return 0, err
	}
	b, _, _ := procSysAllocString.Call(uintptr(unsafe.Pointer(p)))
	if b == 0 {
		return 0, errors.New("com: SysAllocString failed")
	}
	return b, nil
}

func bstrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	n, _, _ := procSysStringLen.Call(uintptr(unsafe.Pointer(p)))
	if n == 0 {
		return ""
	}
	return string(utf16.Decode(unsafe.Slice(p, int(n))))
}

func freeBSTR(p *uint16) {
	if p != nil {
		procSysFreeString.Call(uintptr(unsafe.Pointer(p)))
	}
}

// toVariant converts a Go value to a VARIANT. The returned cleanup frees any
// BSTR allocated for the call (dispatch arguments are borrowed, not AddRef'd).
func toVariant(v any) (variant, func(), error) {
	var r variant
	switch x := v.(type) {
	case nil:
		r.vt = vtEmpty
	case missingArg:
		r.vt = vtError
		r.val = dispEParamNotFound
	case bool:
		r.vt = vtBool
		if x {
			r.val = 0xFFFF // VARIANT_TRUE
		}
	case int:
		return intVariant(int64(x))
	case int8:
		return intVariant(int64(x))
	case int16:
		return intVariant(int64(x))
	case int32:
		return intVariant(int64(x))
	case int64:
		return intVariant(x)
	case uint8:
		return intVariant(int64(x))
	case uint16:
		return intVariant(int64(x))
	case uint32:
		return intVariant(int64(x))
	case float32:
		r.vt = vtR8
		r.val = int64(math.Float64bits(float64(x)))
	case float64:
		r.vt = vtR8
		r.val = int64(math.Float64bits(x))
	case string:
		b, err := sysAllocString(x)
		if err != nil {
			return r, nil, err
		}
		r.vt = vtBSTR
		*(*uintptr)(unsafe.Pointer(&r.val)) = b
		return r, func() { procSysFreeString.Call(b) }, nil
	case *Dispatch:
		r.vt = vtDispatch
		if x != nil && x.p != nil {
			*(**iDispatch)(unsafe.Pointer(&r.val)) = x.p
		}
	default:
		return r, nil, fmt.Errorf("com: unsupported argument type %T", v)
	}
	return r, nil, nil
}

func intVariant(x int64) (variant, func(), error) {
	var r variant
	if x >= math.MinInt32 && x <= math.MaxInt32 {
		r.vt = vtI4
		r.val = int64(uint32(int32(x)))
	} else {
		r.vt = vtI8
		r.val = x
	}
	return r, nil, nil
}

// fromVariant converts a result VARIANT to a Go value and takes ownership of
// its contents (BSTRs are freed, interfaces are wrapped in *Dispatch).
func fromVariant(v *variant) (any, error) {
	switch v.vt {
	case vtEmpty, vtNull:
		return nil, nil
	case vtI2:
		return int64(int16(v.val)), nil
	case vtI4, vtInt, vtError:
		return int64(int32(v.val)), nil
	case vtI1:
		return int64(int8(v.val)), nil
	case vtUI1:
		return int64(uint8(v.val)), nil
	case vtUI2:
		return int64(uint16(v.val)), nil
	case vtUI4, vtUint:
		return int64(uint32(v.val)), nil
	case vtI8, vtUI8:
		return v.val, nil
	case vtR4:
		return float64(math.Float32frombits(uint32(v.val))), nil
	case vtR8, vtDate:
		return math.Float64frombits(uint64(v.val)), nil
	case vtCY:
		return float64(v.val) / 10000, nil
	case vtBool:
		return int16(v.val) != 0, nil
	case vtBSTR:
		p := *(**uint16)(unsafe.Pointer(&v.val))
		s := bstrToString(p)
		freeBSTR(p)
		return s, nil
	case vtDispatch:
		p := *(**iDispatch)(unsafe.Pointer(&v.val))
		if p == nil {
			return nil, nil
		}
		return newDispatch(p), nil
	case vtUnknown:
		u := *(**iUnknown)(unsafe.Pointer(&v.val))
		if u == nil {
			return nil, nil
		}
		defer u.release()
		d, err := u.queryDispatch()
		if err != nil {
			return nil, err
		}
		return newDispatch(d), nil
	}
	vt := v.vt
	procVariantClear.Call(uintptr(unsafe.Pointer(v)))
	return Unsupported{VT: vt}, nil
}

// ---- interfaces ---------------------------------------------------------------

type iUnknown struct{ vtbl *unknownVtbl }

type unknownVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

func (u *iUnknown) release() {
	syscall.SyscallN(u.vtbl.Release, uintptr(unsafe.Pointer(u)))
}

func (u *iUnknown) queryDispatch() (*iDispatch, error) {
	var d *iDispatch
	hr, _, _ := syscall.SyscallN(u.vtbl.QueryInterface, uintptr(unsafe.Pointer(u)),
		uintptr(unsafe.Pointer(&iidIDispatch)), uintptr(unsafe.Pointer(&d)))
	if failed(hr) {
		return nil, &Error{Op: "QueryInterface(IDispatch)", HRESULT: uint32(hr)}
	}
	return d, nil
}

type iDispatch struct{ vtbl *dispatchVtbl }

type dispatchVtbl struct {
	QueryInterface   uintptr
	AddRef           uintptr
	Release          uintptr
	GetTypeInfoCount uintptr
	GetTypeInfo      uintptr
	GetIDsOfNames    uintptr
	Invoke           uintptr
}

// Dispatch is an IDispatch automation object.
//
// Objects created while a worker job runs are released automatically when
// the job ends; call Keep to retain one across jobs (and Release it yourself).
type Dispatch struct {
	p *iDispatch
}

// scope tracks objects created during one worker job.
type scope struct{ items []*Dispatch }

// currentScope is only touched from the worker thread.
var currentScope *scope

func newDispatch(p *iDispatch) *Dispatch {
	d := &Dispatch{p: p}
	if currentScope != nil {
		currentScope.items = append(currentScope.items, d)
	}
	return d
}

func (s *scope) releaseAll() {
	for i := len(s.items) - 1; i >= 0; i-- {
		if s.items[i] != nil {
			s.items[i].Release()
		}
	}
	s.items = nil
}

// Keep detaches d from the automatic release of the current job.
func (d *Dispatch) Keep() *Dispatch {
	if currentScope != nil {
		for i, x := range currentScope.items {
			if x == d {
				currentScope.items[i] = nil
			}
		}
	}
	return d
}

// Release releases the object. Safe to call more than once.
func (d *Dispatch) Release() {
	if d == nil || d.p == nil {
		return
	}
	syscall.SyscallN(d.p.vtbl.Release, uintptr(unsafe.Pointer(d.p)))
	d.p = nil
}

func (d *Dispatch) dispid(name string) (int32, error) {
	if d == nil || d.p == nil {
		return 0, fmt.Errorf("cannot access %q: object is Nothing", name)
	}
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	var id int32
	hr, _, _ := syscall.SyscallN(d.p.vtbl.GetIDsOfNames, uintptr(unsafe.Pointer(d.p)),
		uintptr(unsafe.Pointer(&iidNull)), uintptr(unsafe.Pointer(&namePtr)), 1,
		lcidEnUS, uintptr(unsafe.Pointer(&id)))
	runtime.KeepAlive(namePtr)
	if failed(hr) {
		return 0, &Error{Op: "GetIDsOfNames " + name, HRESULT: uint32(hr)}
	}
	return id, nil
}

func (d *Dispatch) invoke(name string, flags uint16, args []any) (any, error) {
	id, err := d.dispid(name)
	if err != nil {
		return nil, err
	}
	n := len(args)
	vargs := make([]variant, n+1) // +1 so &vargs[0] is valid for n == 0
	var cleanups []func()
	defer func() {
		for _, c := range cleanups {
			c()
		}
	}()
	for i, a := range args {
		v, cleanup, err := toVariant(a)
		if err != nil {
			return nil, fmt.Errorf("%s: argument %d: %v", name, i+1, err)
		}
		vargs[n-1-i] = v // arguments are passed in reverse order
		if cleanup != nil {
			cleanups = append(cleanups, cleanup)
		}
	}
	params := &dispParams{cArgs: uint32(n)}
	if n > 0 {
		params.rgvarg = &vargs[0]
	}
	putID := new(int32)
	*putID = dispidPropertyPut
	isPut := flags&(dispatchPropertyPut|dispatchPropertyPutRef) != 0
	if isPut {
		params.rgdispidNamedArgs = putID
		params.cNamedArgs = 1
	}
	result := new(variant)
	setCurrent(name)
	defer setCurrent("")
	resultPtr := uintptr(unsafe.Pointer(result))
	if isPut {
		resultPtr = 0
	}
	for attempt := 0; ; attempt++ {
		excep := new(excepInfo)
		argErr := new(uint32)
		hr, _, _ := syscall.SyscallN(d.p.vtbl.Invoke, uintptr(unsafe.Pointer(d.p)),
			uintptr(id), uintptr(unsafe.Pointer(&iidNull)), lcidEnUS, uintptr(flags),
			uintptr(unsafe.Pointer(params)), resultPtr,
			uintptr(unsafe.Pointer(excep)), uintptr(unsafe.Pointer(argErr)))
		runtime.KeepAlive(vargs)
		runtime.KeepAlive(putID)
		if !failed(hr) {
			break
		}
		code := uint32(hr)
		if (code == rpcECallRejected || code == rpcEServerCallRetryLate) && attempt < maxBusyRetries {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		return nil, invokeError(name, code, excep, *argErr, n)
	}
	if isPut {
		return nil, nil
	}
	return fromVariant(result)
}

func invokeError(name string, hr uint32, ex *excepInfo, argErr uint32, nargs int) error {
	e := &Error{Op: name, HRESULT: hr}
	if hr == dispEException {
		if ex.pfnDeferredFillIn != 0 {
			syscall.SyscallN(ex.pfnDeferredFillIn, uintptr(unsafe.Pointer(ex)))
		}
		e.Source = bstrToString(ex.bstrSource)
		e.Description = bstrToString(ex.bstrDescription)
		if ex.scode != 0 {
			e.HRESULT = uint32(ex.scode)
		}
		freeBSTR(ex.bstrSource)
		freeBSTR(ex.bstrDescription)
		freeBSTR(ex.bstrHelpFile)
	}
	if (hr == dispETypeMismatch || hr == dispEParamNotFound) && int(argErr) < nargs {
		e.Description = fmt.Sprintf("%s (argument %d)", hresultText(hr), nargs-int(argErr))
	}
	return e
}

// Call invokes a method (or a parameterised property get).
func (d *Dispatch) Call(name string, args ...any) (any, error) {
	return d.invoke(name, dispatchMethod|dispatchPropertyGet, args)
}

// Method invokes a method with DISPATCH_METHOD only (some servers reject the
// combined METHOD|PROPERTYGET flags for methods with optional parameters).
func (d *Dispatch) Method(name string, args ...any) (any, error) {
	return d.invoke(name, dispatchMethod, args)
}

// Get reads a property (optionally with index arguments).
func (d *Dispatch) Get(name string, args ...any) (any, error) {
	return d.invoke(name, dispatchPropertyGet|dispatchMethod, args)
}

// Put writes a property. The last argument is the new value.
func (d *Dispatch) Put(name string, args ...any) error {
	if len(args) == 0 {
		return fmt.Errorf("Put %s: missing value", name)
	}
	flags := uint16(dispatchPropertyPut)
	if _, isObj := args[len(args)-1].(*Dispatch); isObj {
		flags = dispatchPropertyPutRef
	}
	_, err := d.invoke(name, flags, args)
	return err
}

// ErrNothing is returned by Obj helpers when a property returned Nothing.
var ErrNothing = errors.New("object is Nothing")

func asObj(name string, v any, err error) (*Dispatch, error) {
	if err != nil {
		return nil, err
	}
	switch x := v.(type) {
	case *Dispatch:
		return x, nil
	case nil:
		return nil, fmt.Errorf("%s: %w", name, ErrNothing)
	}
	return nil, fmt.Errorf("%s: expected an object, got %T", name, v)
}

// GetObj reads an object property; Nothing is an error wrapping ErrNothing.
func (d *Dispatch) GetObj(name string, args ...any) (*Dispatch, error) {
	v, err := d.Get(name, args...)
	return asObj(name, v, err)
}

// CallObj calls a method returning an object; Nothing is an error wrapping ErrNothing.
func (d *Dispatch) CallObj(name string, args ...any) (*Dispatch, error) {
	v, err := d.Call(name, args...)
	return asObj(name, v, err)
}

// GetObjOrNil reads an object property and returns nil (no error) for Nothing.
func (d *Dispatch) GetObjOrNil(name string, args ...any) (*Dispatch, error) {
	v, err := d.Get(name, args...)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	return asObj(name, v, nil)
}

// GetString reads a property as a string.
func (d *Dispatch) GetString(name string, args ...any) (string, error) {
	v, err := d.Get(name, args...)
	if err != nil {
		return "", err
	}
	return ToString(v), nil
}

// GetInt reads a property as an integer.
func (d *Dispatch) GetInt(name string, args ...any) (int64, error) {
	v, err := d.Get(name, args...)
	if err != nil {
		return 0, err
	}
	n, ok := ToInt(v)
	if !ok {
		return 0, fmt.Errorf("%s: expected a number, got %T", name, v)
	}
	return n, nil
}

// GetFloat reads a property as a float.
func (d *Dispatch) GetFloat(name string, args ...any) (float64, error) {
	v, err := d.Get(name, args...)
	if err != nil {
		return 0, err
	}
	f, ok := ToFloat(v)
	if !ok {
		return 0, fmt.Errorf("%s: expected a number, got %T", name, v)
	}
	return f, nil
}

// GetBool reads a property as a bool.
func (d *Dispatch) GetBool(name string, args ...any) (bool, error) {
	v, err := d.Get(name, args...)
	if err != nil {
		return false, err
	}
	switch x := v.(type) {
	case bool:
		return x, nil
	case int64:
		return x != 0, nil
	}
	return false, fmt.Errorf("%s: expected a boolean, got %T", name, v)
}

// ToString converts a result value to a string.
func ToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	return fmt.Sprint(v)
}

// ToInt converts a numeric result value to int64.
func ToInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(math.Round(x)), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// ToFloat converts a numeric result value to float64.
func ToFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// ---- activation ----------------------------------------------------------------

func clsidFromProgID(progID string) (*GUID, error) {
	p, err := syscall.UTF16PtrFromString(progID)
	if err != nil {
		return nil, err
	}
	clsid := new(GUID)
	hr, _, _ := procCLSIDFromProgID.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(clsid)))
	if failed(hr) {
		return nil, &Error{Op: "CLSIDFromProgID " + progID, HRESULT: uint32(hr)}
	}
	return clsid, nil
}

// GetActiveObject connects to a running application registered in the ROT.
func GetActiveObject(progID string) (*Dispatch, error) {
	clsid, err := clsidFromProgID(progID)
	if err != nil {
		return nil, err
	}
	var unk *iUnknown
	hr, _, _ := procGetActiveObject.Call(uintptr(unsafe.Pointer(clsid)), 0, uintptr(unsafe.Pointer(&unk)))
	if failed(hr) {
		return nil, &Error{Op: "GetActiveObject " + progID, HRESULT: uint32(hr)}
	}
	defer unk.release()
	d, err := unk.queryDispatch()
	if err != nil {
		return nil, err
	}
	return newDispatch(d), nil
}

// CreateObject starts (or connects to) an application via CoCreateInstance.
func CreateObject(progID string) (*Dispatch, error) {
	clsid, err := clsidFromProgID(progID)
	if err != nil {
		return nil, err
	}
	var d *iDispatch
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(clsid)), 0,
		clsctxLocalServer|clsctxInprocServer, uintptr(unsafe.Pointer(&iidIDispatch)),
		uintptr(unsafe.Pointer(&d)))
	if failed(hr) {
		return nil, &Error{Op: "CoCreateInstance " + progID, HRESULT: uint32(hr)}
	}
	return newDispatch(d), nil
}

// ---- message pump ----------------------------------------------------------------

type msg struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	ptX      int32
	ptY      int32
	lPrivate uint32
}

// pumpMessages dispatches pending window messages for this thread. An STA
// thread must do this, otherwise other programs that broadcast messages (or
// COM itself) can hang waiting for it.
func pumpMessages() {
	var m msg
	for {
		r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1 /* PM_REMOVE */)
		if r == 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
