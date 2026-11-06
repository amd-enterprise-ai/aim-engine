// MIT License
//
// Copyright (c) 2026 Advanced Micro Devices, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package main

// gpuModel describes one AMD GPU model as the kernel driver reports it.
// Device IDs follow libdrm's amdgpu.ids; compute units, memory and partition
// modes follow AMD's specifications. Memory is what KFD reports, slightly
// below the marketed size.
type gpuModel struct {
	// deviceID is the PCI device ID.
	deviceID string
	// productName is the sysfs product_name, read from the board's FRU EEPROM.
	// Radeon boards have none.
	productName string
	// marketName is libdrm's marketing name, which the GPU Operator's node
	// labeller uses for devices without a product_name, such as partitions.
	marketName string
	// aimModel is the model token AIM Engine's AcceleratorDetector labels
	// nodes with (see KnownAmdDevices in internal/utils/resources.go).
	aimModel string
	// gfxTargetVersion is KFD's gfx_target_version (major*10000 + minor*100 + stepping).
	gfxTargetVersion int
	computeUnits     int
	simdPerCU        int
	// xcc is the number of compute dies, which is the CPX partition count.
	xcc     int
	vramMiB int64
	// computeModes and memoryModes are the partition modes amdgpu offers;
	// empty for GPUs that cannot be partitioned.
	computeModes []string
	memoryModes  []string
}

var instinctComputeModes = []string{"spx", "dpx", "qpx", "cpx"}

var gpuModels = map[string]gpuModel{
	"mi355x": {
		deviceID:         "0x75a3",
		productName:      "AMD Instinct MI355X OAM",
		marketName:       "AMD Instinct MI355X",
		aimModel:         "MI355X",
		gfxTargetVersion: 90500,
		computeUnits:     256,
		simdPerCU:        4,
		xcc:              8,
		vramMiB:          294896,
		computeModes:     []string{"spx", "dpx", "cpx"},
		memoryModes:      []string{"nps1", "nps2"},
	},
	"mi325x": {
		deviceID:    "0x74a5",
		productName: "AMD Instinct MI325X OAM",
		// What AMD SMI and libdrm report for this card.
		marketName:       "AMD Radeon Graphics",
		aimModel:         "MI325X",
		gfxTargetVersion: 90402,
		computeUnits:     304,
		simdPerCU:        4,
		xcc:              8,
		vramMiB:          262128,
		computeModes:     instinctComputeModes,
		memoryModes:      []string{"nps1", "nps4"},
	},
	"mi300x": {
		deviceID:         "0x74a1",
		productName:      "AMD Instinct MI300X OAM",
		marketName:       "AMD Instinct MI300X",
		aimModel:         "MI300X",
		gfxTargetVersion: 90402,
		computeUnits:     304,
		simdPerCU:        4,
		xcc:              8,
		vramMiB:          196592,
		computeModes:     instinctComputeModes,
		memoryModes:      []string{"nps1", "nps4"},
	},
	"mi210": {
		deviceID:         "0x740f",
		productName:      "AMD Instinct MI210",
		marketName:       "AMD Instinct MI210",
		aimModel:         "MI210",
		gfxTargetVersion: 90010,
		computeUnits:     104,
		simdPerCU:        4,
		xcc:              1,
		vramMiB:          65520,
	},
	"w7900": {
		deviceID:         "0x7448",
		marketName:       "AMD Radeon Pro W7900",
		aimModel:         "W7900",
		gfxTargetVersion: 110000,
		computeUnits:     96,
		simdPerCU:        2,
		xcc:              1,
		vramMiB:          49136,
	},
	"rx7900xtx": {
		deviceID:         "0x744c",
		marketName:       "AMD Radeon RX 7900 XTX",
		aimModel:         "RX7900",
		gfxTargetVersion: 110000,
		computeUnits:     96,
		simdPerCU:        2,
		xcc:              1,
		vramMiB:          24560,
	},
	"r9700": {
		deviceID:         "0x7551",
		marketName:       "AMD Radeon AI PRO R9700",
		aimModel:         "R9700",
		gfxTargetVersion: 120001,
		computeUnits:     64,
		simdPerCU:        2,
		xcc:              1,
		vramMiB:          32752,
	},
}
