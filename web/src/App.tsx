import { lazy, Suspense } from 'react'
import { VerifyEmailPage } from './EmailVerify'
import { SessionLoading } from './shared/ui'

// The admin console and the customer portal load separately, so each side
// downloads only its own code.
const AdminApp = lazy(() => import('./admin/AdminApp').then(module => ({ default: module.AdminApp })))
const CustomerPortalApp = lazy(() => import('./portal/PortalApp').then(module => ({ default: module.CustomerPortalApp })))

export function App() {
  // Customers land on the site root; the merchant console lives under /admin.
  if (window.location.pathname === '/portal/verify-email') return <VerifyEmailPage />
  const admin = window.location.pathname.startsWith('/admin')
  return (
    <Suspense fallback={<SessionLoading portal={admin ? 'admin' : 'customer'} />}>
      {admin ? <AdminApp /> : <CustomerPortalApp />}
    </Suspense>
  )
}
