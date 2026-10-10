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

package temporal

import (
	"context"
	"errors"
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	sdktemporal "go.temporal.io/sdk/temporal"
)

// ErrSearchAttributePermission is returned by EnsureSearchAttributes
// when the caller may not add search attributes to the namespace.
var ErrSearchAttributePermission = errors.New(
	"temporal: no permission to add search attributes; register them on the namespace as an admin " +
		"(temporal operator search-attribute create) or grant the worker that permission")

// EnsureSearchAttributes registers keys as custom search attributes on
// namespace, adding only the missing ones, so it
// is safe on every start.
func EnsureSearchAttributes(ctx context.Context, c client.Client, namespace string, keys []sdktemporal.SearchAttributeKey) error {
	ops := c.OperatorService()
	have, err := ops.ListSearchAttributes(ctx, &operatorservice.ListSearchAttributesRequest{Namespace: namespace})
	if err != nil {
		return searchAttributeErr("list", err)
	}
	missing := make(map[string]enumspb.IndexedValueType)
	for _, k := range keys {
		if _, ok := have.GetCustomAttributes()[k.GetName()]; ok {
			continue
		}
		if _, ok := have.GetSystemAttributes()[k.GetName()]; ok {
			continue
		}
		missing[k.GetName()] = k.GetValueType()
	}
	if len(missing) == 0 {
		return nil
	}
	_, err = ops.AddSearchAttributes(ctx, &operatorservice.AddSearchAttributesRequest{
		Namespace: namespace, SearchAttributes: missing,
	})
	var exists *serviceerror.AlreadyExists
	if err != nil && !errors.As(err, &exists) {
		return searchAttributeErr("add", err)
	}
	return nil
}

func searchAttributeErr(op string, err error) error {
	var denied *serviceerror.PermissionDenied
	if errors.As(err, &denied) {
		return fmt.Errorf("%w: %w", ErrSearchAttributePermission, err)
	}
	return fmt.Errorf("temporal: %s search attributes: %w", op, err)
}
