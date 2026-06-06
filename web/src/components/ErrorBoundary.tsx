import { Component, ReactNode } from 'react'
import { AlertCircle, X } from 'lucide-react'

type Props = {
  readonly children: ReactNode
  readonly onReset?: () => void
  readonly title?: string
}

type State = {
  readonly error: Error | null
}

/**
 * Catches render-phase errors in subtree so a buggy modal doesn't blank the whole app.
 * Shows a recoverable message + reset button.
 */
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: { componentStack: string }) {
    // Keep this visible in dev tools — silent failures are worse than noisy ones
    console.error('ErrorBoundary caught:', error, info.componentStack)
  }

  reset = () => {
    this.setState({ error: null })
    this.props.onReset?.()
  }

  render() {
    if (this.state.error) {
      return (
        <div className="fixed inset-0 bg-black/80 backdrop-blur-sm flex items-center justify-center z-50 p-4">
          <div className="bg-surface-secondary rounded-2xl border border-red-500/30 w-full max-w-md p-5 shadow-2xl">
            <div className="flex items-center justify-between mb-3">
              <h2 className="text-lg font-semibold text-red-400 flex items-center gap-2">
                <AlertCircle className="w-5 h-5" />
                {this.props.title || 'Erro no componente'}
              </h2>
              <button onClick={this.reset} className="text-text-secondary hover:text-text-primary">
                <X className="w-5 h-5" />
              </button>
            </div>
            <p className="text-sm text-text-primary mb-2">A interface encontrou um erro inesperado.</p>
            <pre className="text-xs text-red-300/80 bg-surface rounded p-3 overflow-auto max-h-40">
              {this.state.error.message}
            </pre>
            <button onClick={this.reset} className="btn-primary mt-4 w-full">
              Fechar
            </button>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}
