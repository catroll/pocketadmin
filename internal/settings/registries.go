package settings

import "sync"

// The two registries are built once and shared. They are read-only after
// construction, so the only state to protect is their creation.
var (
	adminOnce   sync.Once
	adminReg    *Registry
	productOnce sync.Once
	productReg  *Registry
)

// Admin returns the back-office registry.
func Admin() *Registry {
	adminOnce.Do(func() { adminReg = newRegistry(SurfaceAdmin, adminKeys()) })
	return adminReg
}

// Product returns the customer-facing registry.
func Product() *Registry {
	productOnce.Do(func() { productReg = newRegistry(SurfaceProduct, productKeys()) })
	return productReg
}

// For returns the registry for a surface.
func For(surface Surface) (*Registry, bool) {
	switch surface {
	case SurfaceAdmin:
		return Admin(), true
	case SurfaceProduct:
		return Product(), true
	default:
		return nil, false
	}
}

// Surfaces returns both surfaces, in a stable order.
func Surfaces() []Surface { return []Surface{SurfaceAdmin, SurfaceProduct} }
