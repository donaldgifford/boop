/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package store

// Scope is the owners an API read may see (DESIGN-0004 OQ8): every
// owner when All, else the installation account logins in Owners,
// lowercased. The zero Scope sees nothing. Every API read takes one and
// filters in SQL, so a row outside it is indistinguishable from a
// missing one.
type Scope struct {
	All    bool
	Owners []string
}
