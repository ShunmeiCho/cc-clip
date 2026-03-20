//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	gdi32DLL = syscall.NewLazyDLL("gdi32.dll")

	procCreateCompatibleDC     = gdi32DLL.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32DLL.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32DLL.NewProc("SelectObject")
	procDeleteDC               = gdi32DLL.NewProc("DeleteDC")
	procDeleteObject           = gdi32DLL.NewProc("DeleteObject")
	procSetPixel               = gdi32DLL.NewProc("SetPixel")

	procGetDC        = user32DLL.NewProc("GetDC")
	procReleaseDC    = user32DLL.NewProc("ReleaseDC")
	procCreateIconIndirect = user32DLL.NewProc("CreateIconIndirect")
	procDestroyIconProc    = user32DLL.NewProc("DestroyIcon")
)

type iconInfo struct {
	fIcon    uint32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

// createColorIcon generates a 16x16 solid-color icon using GDI.
func createColorIcon(r, g, b byte) (uintptr, error) {
	// Get screen DC
	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return 0, fmt.Errorf("GetDC failed")
	}
	defer procReleaseDC.Call(0, screenDC)

	// Create color bitmap
	colorBmp, _, _ := procCreateCompatibleBitmap.Call(screenDC, 16, 16)
	if colorBmp == 0 {
		return 0, fmt.Errorf("CreateCompatibleBitmap for color failed")
	}

	// Create mask bitmap (monochrome)
	maskDC, _, _ := procCreateCompatibleDC.Call(0)
	if maskDC == 0 {
		procDeleteObject.Call(colorBmp)
		return 0, fmt.Errorf("CreateCompatibleDC for mask failed")
	}
	maskBmp, _, _ := procCreateCompatibleBitmap.Call(maskDC, 16, 16)
	if maskBmp == 0 {
		procDeleteDC.Call(maskDC)
		procDeleteObject.Call(colorBmp)
		return 0, fmt.Errorf("CreateCompatibleBitmap for mask failed")
	}
	procDeleteDC.Call(maskDC)

	// Fill color bitmap with solid color
	colorDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if colorDC == 0 {
		procDeleteObject.Call(colorBmp)
		procDeleteObject.Call(maskBmp)
		return 0, fmt.Errorf("CreateCompatibleDC for color failed")
	}
	procSelectObject.Call(colorDC, colorBmp)

	color := uintptr(uint32(r) | uint32(g)<<8 | uint32(b)<<16)

	// Draw a rounded-ish icon: fill all pixels, but skip corners for a softer look
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			// Skip extreme corners for a slightly rounded appearance
			if (x == 0 || x == 15) && (y == 0 || y == 15) {
				continue
			}
			procSetPixel.Call(colorDC, uintptr(x), uintptr(y), color)
		}
	}
	procDeleteDC.Call(colorDC)

	// Fill mask bitmap (0 = opaque, matching color bitmap)
	maskDC2, _, _ := procCreateCompatibleDC.Call(0)
	if maskDC2 != 0 {
		procSelectObject.Call(maskDC2, maskBmp)
		// Monochrome bitmap: 0 = black = opaque for color icon
		// SetPixel with 0 (black) for opaque pixels, 0xFFFFFF (white) for transparent
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				if (x == 0 || x == 15) && (y == 0 || y == 15) {
					procSetPixel.Call(maskDC2, uintptr(x), uintptr(y), 0x00FFFFFF) // transparent
				}
				// Default is black (opaque) for monochrome bitmap created from DC(0)
			}
		}
		procDeleteDC.Call(maskDC2)
	}

	// Create icon
	ii := iconInfo{
		fIcon:    1, // TRUE = icon
		hbmMask:  maskBmp,
		hbmColor: colorBmp,
	}
	icon, _, err := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))

	// Clean up bitmaps (icon keeps its own copy)
	procDeleteObject.Call(colorBmp)
	procDeleteObject.Call(maskBmp)

	if icon == 0 {
		return 0, fmt.Errorf("CreateIconIndirect: %w", err)
	}
	return icon, nil
}

// destroyIcon releases an icon handle.
func destroyIcon(icon uintptr) {
	procDestroyIconProc.Call(icon)
}
