import { message as antdMessage } from 'antd'
import type { MessageInstance } from 'antd/es/message/interface'

let _message: MessageInstance = antdMessage

export function setMessage(m: MessageInstance) { _message = m }
export function getMessage() { return _message }
