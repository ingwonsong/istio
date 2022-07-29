/*
Copyright 2018 The Kubernetes Authors.

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

package set

type (
	Empty struct{}
	T     interface{}
	Set   map[T]Empty
)

func (s Set) Has(item T) bool {
	_, exists := s[item]
	return exists
}

func (s Set) Insert(items ...T) {
	for _, item := range items {
		s[item] = Empty{}
	}
}

func (s Set) Delete(item T) {
	delete(s, item)
}

func (s Set) Length() int {
	return len(s)
}