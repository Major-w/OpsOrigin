
import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'

export default function InboundRedirect() {
  const navigate = useNavigate()
  useEffect(() => { navigate('/itsm/inbound', { replace: true }) }, [navigate])
  return null
}
