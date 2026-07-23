cat << 'INNER_EOF' > temp_patch.go
          <div class="command-item">
            <code>cc-dialect doctor [--fix]</code>
            <span>Diagnose configuration issues and apply deterministic repairs</span>
          </div>
INNER_EOF
sed -i '' -e '/<div class="command-item">/,/<span>Diagnose configuration issues<\/span>/d' landing/reference.html
sed -i '' -e '/<\/div>/d' landing/reference.html
# let's write it cleaner.
