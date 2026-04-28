// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package recipes

import (
	"embed"
)

// Only embed the files actually read by pkg/recipe at runtime: the registry,
// recipe overlays (including base and leaves), composable mixins, and per-
// component default Helm values. AICR's checks/, validators/, and component
// manifests/ directories are not consumed by this provider.
//
//go:embed registry.yaml overlays/*.yaml mixins/*.yaml components/*/values*.yaml
var FS embed.FS
