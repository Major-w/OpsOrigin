import { useTeams } from '@/stores/ai'
const { teams } = useTeams()
teams.map(team => team.id)
