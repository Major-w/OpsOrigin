import { useLocation } from 'react-router-dom'

export default function usePathname() {
  const { pathname } = useLocation()
  return pathname
}
