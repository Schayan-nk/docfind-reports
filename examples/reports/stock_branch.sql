-- id: stock_branch
-- title: موجودی انبار شعبه
-- description: موجودی هر کالا در انبار شعبه و آخرین تغییر آن
-- group: انبار
-- family: warehouses | گزارش انبارها | انبار
-- variant: انبار شعبه
-- chart: hbar | label=item | value=qty
-- monthly: updated_on | آخرین تغییر | calendar=gregorian
-- total: qty
-- column: item | کالا
-- column: qty | موجودی
-- column: updated_on | آخرین تغییر
-- param: pitem | like | کالا
-- suggest: pitem | stock | item
SELECT s.item                             AS item,
       s.qty                              AS qty,
       DATE_FORMAT(s.updated_at, '%Y-%m-%d') AS updated_on
FROM stock s
WHERE s.warehouse = 'branch'
  AND s.item LIKE @pitem
ORDER BY s.item
