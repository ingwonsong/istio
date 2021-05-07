/*
 Copyright Istio Authors

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

// contains classes and functions related to traversing the relationship between pods, control plane revisions, and
// data plane revisions.  Mapper handles traversing the relationship without relying on custom caching. ReadWritePodCache
// leverages caches to provide quick access to the instance count of each proxy version for a given revision, as well
// as an easy way to traverse pods which still need to be updated for a given revision and target version.
package revision
