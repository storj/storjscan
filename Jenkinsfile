pipeline {
    agent {
        docker {
            label 'main'
            image 'storjlabs/ci:latest'
            alwaysPull true
            args '-u root:root --cap-add SYS_PTRACE -v "/tmp/gomod":/go/pkg/mod -v "/tmp/npm":/npm --tmpfs "/tmp:exec,mode=777"'
        }
    }
    options {
        timeout(time: 10, unit: 'MINUTES')
        skipDefaultCheckout(true)
    }
    environment {
        NPM_CONFIG_CACHE = '/npm/cache'
        GOTRACEBACK = 'all'
    }
    stages {
        stage('Checkout') {
            steps {
                // Delete any content left over from a previous run.
                sh "chmod -R 777 ."
                // Bash requires extglob option to support !(.git) syntax,
                // and we don't want to delete .git to have faster clones.
                sh 'bash -O extglob -c "rm -rf !(.git)"'

                checkout scm

                sh 'mkdir -p .build'

                // make a backup of the mod file, because sometimes they get modified by tools
                // this allows to lint the unmodified files
                sh 'cp go.mod .build/go.mod.orig'

                // download dependencies
                sh 'go mod download'

                // pre-check that we cannot do at a later stage reliably
                sh 'check-large-files'
            }
        }
        stage('Build') {
            parallel {
                stage('go') {
                    steps {
                        sh 'go build -v ./...'
                        sh 'go test -v -p 16 -bench XYZXYZXYZXYZ -run XYZXYZXYZXYZ ./...'
                    }
                }
                stage('go -race') {
                    steps {
                        sh 'go build -v -race ./...'
                        sh 'go test -v -p 16 -bench XYZXYZXYZXYZ -run XYZXYZXYZXYZ -race ./...'
                    }
                }
            }
        }

        stage('Verification') {
            parallel {
                stage('Lint') {
                    steps {
                        sh 'check-copyright'
                        sh 'check-imports -race ./...'
                        sh 'check-peer-constraints -race'
                        sh 'check-atomic-align ./...'
                        sh 'check-monkit ./...'
                        sh 'check-errs ./...'
                        sh 'staticcheck ./...'
                        sh 'golangci-lint --config /go/ci/.golangci.yml -j=2 run'
                        sh 'check-mod-tidy -mod .build/go.mod.orig'
                     }
                }
                stage('Tests') {
                    environment {
                        COVERFLAGS = "${ env.BRANCH_NAME == 'main' ? '-coverprofile=.build/coverprofile -coverpkg=storj.io/storjscan/...' : ''}"
                    }
                    steps {
                        sh 'go test -parallel 4 -p 6 -vet=off $COVERFLAGS -timeout 8m -json -race ./... 2>&1 | tee .build/tests.json | xunit -out .build/tests.xml'
                    }

                    post {
                        always {
                            archiveArtifacts artifacts: '.build/tests.json'
                            sh script: 'cat .build/tests.json | tparse -all -top -slow 100', returnStatus: true
                            junit '.build/tests.xml'

                            script {
                                if(fileExists(".build/coverprofile")){
                                    sh script: 'filter-cover-profile < .build/coverprofile > .build/clean.coverprofile', returnStatus: true
                                    sh script: 'gocov convert .build/clean.coverprofile > .build/cover.json', returnStatus: true
                                    sh script: 'gocov-xml  < .build/cover.json > .build/cobertura.xml', returnStatus: true
                                    cobertura coberturaReportFile: '.build/cobertura.xml',
                                        lineCoverageTargets: '70, 60, 50',
                                        autoUpdateHealth: false,
                                        autoUpdateStability: false,
                                        failUnhealthy: true
                                }
                            }
                        }
                    }
                }
            }
        }
        stage('Post') {
            parallel {
                stage('Lint') {
                    steps {
                        sh 'check-clean-directory'
                    }
                }
            }
        }
    }
}
