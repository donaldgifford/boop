import { DataTable, type DataTableColumn } from "@donaldgifford/design-system/blocks/data-table";
import { ErrorState } from "@donaldgifford/design-system/blocks/error-state";
import { InlineEmpty } from "@donaldgifford/design-system/blocks/inline-state";
import { LoadMore } from "@donaldgifford/design-system/blocks/load-more";
import { Card } from "@donaldgifford/design-system/components/card";
import type { ReactNode } from "react";

// ListState is what ListCard needs from useList.
export interface ListState<T> {
  items: readonly T[];
  isPending: boolean;
  isError: boolean;
  error: unknown;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => unknown;
  refetch: () => unknown;
}

interface ListCardProps<T extends Record<string, unknown>> {
  list: ListState<T>;
  columns: ReadonlyArray<DataTableColumn<T>>;
  label: string;
  noun: string;
  getRowId: (row: T) => string;
  onRowClick?: ((row: T) => void) | undefined;
  selectedId?: string | undefined;
  compact?: boolean | undefined;
  empty?: ReactNode | undefined;
}

// ListCard is a cursor-paged list from the API: the table in the order
// the API returns (sorting is the API's, so the table does not re-sort),
// "Load more" for the next page, and the problem when a page fails.
export function ListCard<T extends Record<string, unknown>>({ list, columns, label, noun, getRowId, onRowClick, selectedId, compact, empty }: ListCardProps<T>) {
  if (list.isError) {
    return <ErrorState error={list.error} onRetry={() => void list.refetch()} />;
  }
  return (
    <Card className="gap-0 py-0">
      <DataTable
        columns={columns}
        data={list.items}
        label={label}
        bare
        sortable={false}
        isLoading={list.isPending}
        getRowId={getRowId}
        onRowClick={onRowClick}
        selectedId={selectedId}
        compact={compact}
        empty={empty ?? <InlineEmpty>No {noun} match these filters.</InlineEmpty>}
      />
      {list.items.length > 0 ? (
        <LoadMore loaded={list.items.length} hasMore={list.hasNextPage} loading={list.isFetchingNextPage} onMore={() => void list.fetchNextPage()} noun={noun} />
      ) : null}
    </Card>
  );
}
