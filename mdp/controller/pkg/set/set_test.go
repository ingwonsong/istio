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

package set

import (
	"testing"

	"github.com/onsi/gomega"
)

func TestSet(t *testing.T) {
	s := Set{}
	for i := 0; i < 5; i++ {
		s.Insert(i)
	}
	g := gomega.NewGomegaWithT(t)
	g.Expect(s).To(gomega.HaveLen(5))
	g.Expect(s.Has(2)).To(gomega.BeTrue())
	g.Expect(s.Has(7)).NotTo(gomega.BeTrue())
	s.Delete(2)
	g.Expect(s).To(gomega.HaveLen(4))
	g.Expect(s.Has(2)).NotTo(gomega.BeTrue())
}