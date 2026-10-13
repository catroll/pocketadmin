package demo

// catalogue writes the product categories, products, their variants and the opening
// stock that explains every balance.
//
// The stock ledger is written rather than the cached balance alone, because the
// reconciliation screen compares the two: a demonstration whose ledger and cache disagree
// would show a discrepancy nobody caused.

// catalogue writes categories, products, variants and opening stock.
func (s *Seeder) catalogue() error {
	categories := []struct {
		key, name, slug string
	}{
		{"cat.audio", "Audio", "audio"},
		{"cat.desk", "Desk", "desk"},
		{"cat.travel", "Travel", "travel"},
	}

	for i, category := range categories {
		rec, err := s.create("product_categories", map[string]any{
			"name":        category.name,
			"slug":        category.slug,
			"description": "<p>" + category.name + " things.</p>",
			"status":      "active",
			"sortOrder":   (i + 1) * 10,
		})
		if err != nil {
			return err
		}
		s.remember(category.key, rec)
	}

	type variant struct {
		sku, label string
		price      int
		stock      int
	}
	products := []struct {
		key, name, slug, category, brand string
		price, sale                      int
		variants                         []variant
	}{
		{
			key: "prod.headphones", name: "Studio headphones", slug: "studio-headphones",
			category: "cat.audio", brand: "Northwave", price: 129900, sale: 109900,
			variants: []variant{
				{"DEMO-HP-BLK", "Black", 109900, 24},
				{"DEMO-HP-WHT", "White", 109900, 11},
			},
		},
		{
			key: "prod.speaker", name: "Desk speaker", slug: "desk-speaker",
			category: "cat.audio", brand: "Northwave", price: 79900, sale: 0,
			variants: []variant{
				{"DEMO-SPK-01", "Single", 79900, 40},
			},
		},
		{
			key: "prod.lamp", name: "Reading lamp", slug: "reading-lamp",
			category: "cat.desk", brand: "Bright & Co", price: 34900, sale: 29900,
			variants: []variant{
				{"DEMO-LMP-WARM", "Warm white", 29900, 6},
				{"DEMO-LMP-COOL", "Cool white", 29900, 0},
			},
		},
		{
			key: "prod.notebook", name: "Lined notebook", slug: "lined-notebook",
			category: "cat.desk", brand: "Papermill", price: 4900, sale: 0,
			variants: []variant{
				{"DEMO-NB-A5", "A5", 4900, 200},
			},
		},
		{
			key: "prod.daypack", name: "Day pack", slug: "day-pack",
			category: "cat.travel", brand: "Ridgeline", price: 19900, sale: 0,
			variants: []variant{
				{"DEMO-DP-18", "18 litre", 19900, 15},
				{"DEMO-DP-28", "28 litre", 24900, 9},
			},
		},
		{
			key: "prod.bottle", name: "Insulated bottle", slug: "insulated-bottle",
			category: "cat.travel", brand: "Ridgeline", price: 6900, sale: 5900,
			variants: []variant{
				{"DEMO-BT-500", "500 ml", 5900, 60},
			},
		},
	}

	for i, product := range products {
		rec, err := s.create("products", map[string]any{
			"kind":        "physical",
			"name":        product.name,
			"slug":        product.slug,
			"subtitle":    "Example data",
			"description": "<p>" + product.name + ", from the demo dataset.</p>",
			"category":    s.id(product.category),
			"brand":       product.brand,
			"status":      "on_sale",
			"listPrice":   product.price,
			"salePrice":   product.sale,
			"currency":    "USD",
			"taxable":     true,
			"stockMode":   "variant",
			"sortOrder":   (i + 1) * 10,
		})
		if err != nil {
			return err
		}
		s.remember(product.key, rec)

		for j, v := range product.variants {
			variantRec, err := s.create("product_variants", map[string]any{
				"product":   rec.Id,
				"sku":       v.sku,
				"label":     v.label,
				"listPrice": v.price,
				"currency":  "USD",
				"stock":     0,
				"status":    "active",
				"sortOrder": (j + 1) * 10,
			})
			if err != nil {
				return err
			}
			s.remember(product.key+".variant."+v.label, variantRec)

			// The opening stock goes through the ledger, which sets the cached balance
			// as a side effect. Writing the balance directly would leave a number with
			// nothing explaining it, which is exactly what the reconciliation screen
			// exists to find.
			if v.stock > 0 {
				if err := s.openingStock(variantRec, v.stock); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
