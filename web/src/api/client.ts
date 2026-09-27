export async function apiRequest<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const isFormData = options.body instanceof FormData
  const headers: Record<string, string> = {
    ...(isFormData ? {} : { 'Content-Type': 'application/json' }),
    ...(options.headers as Record<string, string>),
  }

  const response = await fetch(endpoint, {
    ...options,
    headers,
  })

  if (!response.ok) {
    let errorMsg = `HTTP Error ${response.status}`
    try {
      const data = await response.json()
      if (data && data.error) {
        errorMsg = data.error
      }
    } catch {
      // ignore
    }
    throw new Error(errorMsg)
  }

  return response.json()
}
