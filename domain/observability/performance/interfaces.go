/*
 * © 2022 Snyk Limited All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package performance

import "context"

type Instrumentor interface {
	StartSpan(ctx context.Context, operation string) Span
	NewTransaction(ctx context.Context, txName string, operation string) Span
	Finish(span Span)
}

type Span interface {
	SetTransactionName(name string)
	StartSpan(ctx context.Context)
	Finish()
	GetOperation() string
	GetTxName() string

	// GetTraceId Returns UUID of the trace
	GetTraceId() string
	Context() context.Context
}
